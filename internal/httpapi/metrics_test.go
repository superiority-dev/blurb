package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/blurb/internal/queue"
	"github.com/superiority-dev/blurb/internal/testimage"
)

// TestMetrics requires each /hash request that reaches the image read to be
// counted by format and outcome, each queue refusal by queue and reason, each
// decode's estimate and wait to be recorded, the queues' counts and memory
// limit to be observed, and the HTTP metrics to leave the probes out.
func TestMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	hashAPI := testAPI(t)

	hashQueue, err := queue.New(1, queue.Options{})
	if err != nil {
		t.Fatal(err)
	}

	decodeQueue, err := queue.New(1, queue.Options{MaxMemoryBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(
		testConfig(),
		hashAPI.fetcher,
		hashQueue,
		decodeQueue,
		testReady(),
		discardLogger,
		meters,
	)
	if err != nil {
		t.Fatal(err)
	}

	photo := testimage.GradientJPEG(t, 300, 200)

	// Over the 2 MiB of decode memory, under the test config's pixel limit.
	large := testimage.GradientJPEG(t, 1000, 1000)

	// Under the 2 MiB of decode memory, cut by the body limit after its header.
	cut := noiseGIF(t)

	requests := []struct {
		body   []byte
		length int64
		status int
	}{
		{body: photo, length: int64(len(photo)), status: http.StatusOK},
		{body: []byte("not an image"), length: 12, status: http.StatusUnsupportedMediaType},
		{body: large, length: int64(len(large)), status: http.StatusRequestEntityTooLarge},
		{body: cut, length: -1, status: http.StatusRequestEntityTooLarge},
	}

	for _, tc := range requests {
		req := httptest.NewRequest(http.MethodPost, "/hash", bytes.NewReader(tc.body))
		req.ContentLength = tc.length

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != tc.status {
			t.Fatalf("got %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
		}
	}

	release, err := hashQueue.Acquire(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, req := range []struct {
		method, target string
		body           []byte
	}{
		{method: http.MethodPost, target: "/hash", body: photo},
		{method: http.MethodGet, target: "/hash?url=" + url.QueryEscape("http://origin.invalid/photo.jpg")},
	} {
		if rec := serve(h, req.method, req.target, req.body); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s with the hash queue full: got %d %s, want 503", req.method, rec.Code, rec.Body.String())
		}
	}

	if rec := serve(h, http.MethodGet, pathHealthz, nil); rec.Code != http.StatusOK {
		t.Fatalf("healthz: got %d", rec.Code)
	}

	got, sums := collect(t, reader)
	release()

	checkPoints(t, got, map[string]map[string]float64{
		"blurb.hash.requests": {
			"blurb.code=ok,blurb.format=jpeg":                 1,
			"blurb.code=unsupported_media_type,blurb.format=": 1,
			"blurb.code=too_large,blurb.format=jpeg":          1,
			"blurb.code=too_large,blurb.format=gif":           1,
		},
		"blurb.queue.refused": {
			"blurb.queue=decode,blurb.reason=too_large": 1,
			"blurb.queue=hash,blurb.reason=busy":        2,
		},
		"blurb.decode.memory.estimate": {"blurb.format=jpeg": 2, "blurb.format=gif": 1},
		"blurb.decode.wait.duration":   {"": 2},
		"blurb.hash.active":            {"": 1},
		"blurb.decode.active":          {"": 0},
		"blurb.decode.waiting":         {"": 0},
		"blurb.decode.memory.reserved": {"": 0},
		"blurb.decode.memory.limit":    {"": 2 << 20},
	})

	checkPoints(t, sums, map[string]map[string]float64{
		"blurb.decode.memory.estimate": {
			"blurb.format=jpeg": decodeBytes(t, photo) + decodeBytes(t, large),
			"blurb.format=gif":  decodeBytes(t, cut),
		},
	})

	var httpRequests float64
	for _, count := range got["http.server.request.duration"] {
		httpRequests += count
	}

	if httpRequests != 6 {
		t.Errorf("http.server.request.duration counted %v requests, want the 6 to /hash", httpRequests)
	}
}

// TestMetricsDecodeWait requires a request waiting for its decode slot to be
// observed as waiting, and its wait to be recorded in seconds.
func TestMetricsDecodeWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const held = 2 * time.Second

		reader := sdkmetric.NewManualReader()
		meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

		hashAPI := testAPI(t)

		decodeQueue, err := queue.New(1, queue.Options{MaxWaitingRequests: 1})
		if err != nil {
			t.Fatal(err)
		}

		h, err := NewHandler(
			testConfig(),
			hashAPI.fetcher,
			hashAPI.hashQueue,
			decodeQueue,
			testReady(),
			discardLogger,
			meters,
		)
		if err != nil {
			t.Fatal(err)
		}

		release, err := decodeQueue.Acquire(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}

		done := make(chan int)
		go func() { done <- serve(h, http.MethodPost, "/hash", testimage.GradientJPEG(t, 300, 200)).Code }()

		synctest.Wait()

		got, _ := collect(t, reader)
		checkPoints(t, got, map[string]map[string]float64{
			"blurb.hash.active":    {"": 1},
			"blurb.decode.active":  {"": 1},
			"blurb.decode.waiting": {"": 1},
		})

		time.Sleep(held)
		release()

		if code := <-done; code != http.StatusOK {
			t.Fatalf("got %d, want 200", code)
		}

		_, sums := collect(t, reader)
		checkPoints(t, sums, map[string]map[string]float64{
			"blurb.decode.wait.duration": {"": held.Seconds()},
		})
	})
}

// TestMetricsRefuse requires each error a queue's Acquire returns, wrapped or
// not, to be counted under its own reason.
func TestMetricsRefuse(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	hashAPI := testAPI(t)

	instruments, err := newMetrics(meters.Meter(meterName), hashAPI.hashQueue, hashAPI.hasher.decodeQueue)
	if err != nil {
		t.Fatal(err)
	}

	for _, err := range []error{
		queue.ErrBusy,
		fmt.Errorf("decode queue: %w", queue.ErrTimeout),
		queue.ErrTooLarge,
		context.Canceled,
	} {
		instruments.refuse(t.Context(), queueDecode, err)
	}

	got, _ := collect(t, reader)
	checkPoints(t, got, map[string]map[string]float64{
		"blurb.queue.refused": {
			"blurb.queue=decode,blurb.reason=busy":      1,
			"blurb.queue=decode,blurb.reason=timeout":   1,
			"blurb.queue=decode,blurb.reason=too_large": 1,
			"blurb.queue=decode,blurb.reason=canceled":  1,
		},
	})
}

// collect reads every metric from reader, keyed by name and then by its
// attributes in [attribute.DefaultEncoder] form. A sum or gauge gives its
// value and a histogram its count in got, and a histogram its sum in sums.
func collect(t *testing.T, reader *sdkmetric.ManualReader) (got, sums map[string]map[string]float64) {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	encoder := attribute.DefaultEncoder()
	got = map[string]map[string]float64{}
	sums = map[string]map[string]float64{}

	for _, scope := range rm.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			points, pointSums := map[string]float64{}, map[string]float64{}

			switch data := instrument.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					points[point.Attributes.Encoded(encoder)] = float64(point.Value)
				}
			case metricdata.Gauge[int64]:
				for _, point := range data.DataPoints {
					points[point.Attributes.Encoded(encoder)] = float64(point.Value)
				}
			case metricdata.Histogram[int64]:
				for _, point := range data.DataPoints {
					points[point.Attributes.Encoded(encoder)] = float64(point.Count)
					pointSums[point.Attributes.Encoded(encoder)] = float64(point.Sum)
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					points[point.Attributes.Encoded(encoder)] = float64(point.Count)
					pointSums[point.Attributes.Encoded(encoder)] = point.Sum
				}
			}

			got[instrument.Name] = points
			sums[instrument.Name] = pointSums
		}
	}

	return got, sums
}

// checkPoints requires each metric in want to hold exactly the points given.
func checkPoints(t *testing.T, got, want map[string]map[string]float64) {
	t.Helper()

	for name, points := range want {
		for attrs, value := range points {
			if got[name][attrs] != value {
				t.Errorf("%s{%s}: got %v, want %v", name, attrs, got[name][attrs], value)
			}
		}

		if len(got[name]) != len(points) {
			t.Errorf("%s: got points %v, want %v", name, got[name], points)
		}
	}
}

// decodeBytes returns the memory header.Read estimates data needs to decode.
func decodeBytes(t *testing.T, data []byte) float64 {
	t.Helper()

	info, _, err := header.Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	return float64(info.DecodeBytes)
}
