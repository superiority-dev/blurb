package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/superiority-dev/blurb/internal/queue"
	"github.com/superiority-dev/blurb/internal/testimage"
)

func TestHashPost(t *testing.T) {
	h := apiHandler(testAPI(t), discardLogger)

	solid := image.NewRGBA(image.Rect(0, 0, 30, 20))
	for i := 0; i < len(solid.Pix); i += 4 {
		copy(solid.Pix[i:], []uint8{0xc8, 0x1e, 0x0a, 0xff})
	}

	solidRec := serve(h, http.MethodPost, "/hash?formats=blurhash", testimage.EncodePNG(t, solid))
	if !strings.Contains(solidRec.Body.String(), `"color":"#c81e0a"`) {
		t.Errorf("a solid #c81e0a PNG: %s", solidRec.Body.String())
	}

	rec := serve(h, http.MethodPost, "/hash?formats=blurhash", testimage.GradientJPEG(t, 300, 200))
	if rec.Code != http.StatusOK ||
		rec.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(rec.Body.String(), `"blurhash":"`) ||
		strings.Contains(rec.Body.String(), `"thumbhash"`) {
		t.Errorf("got %d %s, Cache-Control %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}

	big := append(testimage.GradientJPEG(t, 300, 200), make([]byte, maxTestBody)...)
	pastLimit := testimage.JPEGPadTo(testimage.GradientJPEG(t, 30, 20), maxTestBody+1)

	for _, tc := range []struct {
		name   string
		body   []byte
		length int64
	}{
		{name: "over the body limit", body: big, length: int64(len(big))},
		{name: "over the body limit without a length", body: pastLimit, length: -1},
		{name: "a GIF cut by the body limit", body: noiseGIF(t), length: -1},
	} {
		req := httptest.NewRequest(http.MethodPost, "/hash", bytes.NewReader(tc.body))
		req.ContentLength = tc.length

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// TestHashPostFirstFrame requires an animation whose first frame does not
// fill its canvas to be hashed at the canvas's size with alpha.
func TestHashPostFirstFrame(t *testing.T) {
	h := apiHandler(testAPI(t), discardLogger)

	cases := []struct {
		name      string
		data      []byte
		thumbHash string
	}{
		{name: "WebP", data: testimage.WebPAnimated(), thumbHash: "VzqDBIAR10d3h4CDd3n4l3ePcI14GOeICw=="},
		{name: "GIF", data: testimage.GIFAnimated(), thumbHash: "VzqDBIARg7pnl5DIanj3hnf/KNGLNXUxBQ=="},
	}

	for _, tc := range cases {
		rec := serve(h, http.MethodPost, "/hash", tc.data)

		var got hashResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil ||
			rec.Code != http.StatusOK ||
			got.Width != 64 ||
			got.Height != 48 ||
			!got.HasAlpha ||
			base64.StdEncoding.EncodeToString(got.ThumbHash) != tc.thumbHash {
			t.Errorf("%s: got %d %s, want 64x48 with alpha and ThumbHash %s",
				tc.name, rec.Code, rec.Body.String(), tc.thumbHash)
		}
	}
}

// TestHashPostUnsupportedFeature requires a JPEG whose subsampling the decoder
// does not implement to answer 415 naming the format and the feature.
func TestHashPostUnsupportedFeature(t *testing.T) {
	sof := []byte{8, 0, 16, 0, 16, 4, 1, 0x21, 0, 2, 0x11, 0, 3, 0x11, 0, 4, 0x21, 0}
	cmyk422 := append([]byte{0xff, 0xd8}, testimage.JPEGSegment(0xc0, sof)...)
	rec := serve(apiHandler(testAPI(t), discardLogger), http.MethodPost, "/hash", cmyk422)

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil ||
		rec.Code != http.StatusUnsupportedMediaType ||
		body.Error.Code != "unsupported_media_type" ||
		body.Error.Message != "the image uses a jpeg feature that is not supported: luma/chroma subsampling ratio" {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}

// TestHashPostRepeated requires a 400 naming formats or extras when the
// query gives it twice.
func TestHashPostRepeated(t *testing.T) {
	h := apiHandler(testAPI(t), discardLogger)

	for _, name := range []string{"formats", "extras"} {
		target := "/hash?formats=blurhash&extras=phash&" + name + "=x"
		rec := serve(h, http.MethodPost, target, testimage.GradientJPEG(t, 30, 20))
		checkRepeated(t, rec, name)
	}
}

// TestHashPostBytes requires the bytes field to be the declared length, or the
// number of bytes read when none is declared, the body to be read to its end
// only when no length is declared or the SHA-256 is asked for, and the
// sha256 field to be the whole input's SHA-256 when asked for and absent
// otherwise.
func TestHashPostBytes(t *testing.T) {
	h := apiHandler(testAPI(t), discardLogger)
	data := append(testimage.GradientJPEG(t, 300, 200), make([]byte, 128<<10)...) // 128 KiB past the image
	sum := sha256.Sum256(data)
	whole := hex.EncodeToString(sum[:])

	cases := []struct {
		target    string
		length    int64
		readToEnd bool
		sha256    string
	}{
		{target: "/hash", length: int64(len(data)), readToEnd: false, sha256: ""},
		{target: "/hash", length: -1, readToEnd: true, sha256: ""},
		{target: "/hash?extras=sha256", length: int64(len(data)), readToEnd: true, sha256: whole},
	}

	for _, tc := range cases {
		body := bytes.NewReader(data)
		req := httptest.NewRequest(http.MethodPost, tc.target, body)
		req.ContentLength = tc.length

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		var got struct {
			Bytes  int
			SHA256 string
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%+v: got %d %s, %v", tc, rec.Code, rec.Body.String(), err)
		}

		if got.Bytes != len(data) {
			t.Errorf("%+v: bytes %d, want %d", tc, got.Bytes, len(data))
		}

		if readToEnd := body.Len() == 0; readToEnd != tc.readToEnd {
			t.Errorf("%+v: read to its end %t, want %t; %d bytes unread", tc, readToEnd, tc.readToEnd, body.Len())
		}

		if got.SHA256 != tc.sha256 {
			t.Errorf("%+v: sha256 %q, want %q", tc, got.SHA256, tc.sha256)
		}
	}
}

// TestHashPostLimits requires an image of exactly [Config.MaxPixels] to be
// hashed and one with a pixel more to be refused, and the queue to be given
// the image's decode estimate.
func TestHashPostLimits(t *testing.T) {
	photo := testimage.GradientJPEG(t, 300, 200)

	// photo's estimate, from header.Read.
	const photoDecodeBytes = 1_143_424

	cases := []struct {
		name              string
		maxPixels, memory int64
		status            int
	}{
		{name: "pixels at the limit", maxPixels: 300 * 200, memory: 0, status: http.StatusOK},
		{name: "a pixel over the limit", maxPixels: 300*200 - 1, memory: 0, status: http.StatusRequestEntityTooLarge},
		{name: "estimate at the memory allowed", maxPixels: 300 * 200, memory: photoDecodeBytes, status: http.StatusOK},
		{
			name:      "estimate a byte over the memory allowed",
			maxPixels: 300 * 200,
			memory:    photoDecodeBytes - 1,
			status:    http.StatusRequestEntityTooLarge,
		},
	}

	for _, tc := range cases {
		hashAPI := testAPI(t)
		hashAPI.hasher.maxPixels = tc.maxPixels

		var err error
		hashAPI.hasher.decodeQueue, err = queue.New(1, queue.Options{MaxMemoryBytes: tc.memory})
		if err != nil {
			t.Fatal(err)
		}

		if rec := serve(apiHandler(hashAPI, discardLogger), http.MethodPost, "/hash", photo); rec.Code != tc.status {
			t.Errorf("%s: got %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
		}
	}
}

// TestHashPostBusy requires a POST to answer 503 before reading its body while
// the hash queue is full, and each request to give its place back.
func TestHashPostBusy(t *testing.T) {
	hashAPI := testAPI(t)

	var err error
	hashAPI.hashQueue, err = queue.New(1, queue.Options{})
	if err != nil {
		t.Fatal(err)
	}

	h := apiHandler(hashAPI, discardLogger)

	release, err := hashAPI.hashQueue.Acquire(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if rec := serve(h, http.MethodPost, "/hash", []byte("not an image")); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("while full: got %d %s, want 503", rec.Code, rec.Body.String())
	}

	release()

	photo := testimage.GradientJPEG(t, 300, 200)
	for i := range 2 {
		if rec := serve(h, http.MethodPost, "/hash", photo); rec.Code != http.StatusOK {
			t.Errorf("request %d after the release: got %d %s, want 200", i+1, rec.Code, rec.Body.String())
		}
	}
}

// TestHashPostReset requires a POST whose client resets the connection
// mid-body to be logged as a 503 at info.
func TestHashPostReset(t *testing.T) {
	logger, logs := jsonLogger()
	active := make(chan struct{})
	srv := httptest.NewUnstartedServer(apiHandler(testAPI(t), logger))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateActive {
			close(active)
		}
	}
	srv.Start()

	photo := testimage.GradientJPEG(t, 300, 200)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	fmt.Fprintf(
		conn,
		"POST /hash HTTP/1.1\r\nHost: blurb\r\nContent-Length: %d\r\n\r\n%s",
		len(photo),
		photo[:len(photo)/2],
	)
	<-active

	if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
		t.Fatal(err)
	}

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	srv.Close()

	checkLog(t, logs, map[string]any{
		"status":      float64(http.StatusServiceUnavailable),
		slog.LevelKey: slog.LevelInfo.String(),
	})
}

// TestHashPostBodyErrors requires a body that ends before its declared length,
// or whose chunked encoding is broken, to answer 400 bad_request saying which.
func TestHashPostBodyErrors(t *testing.T) {
	srv := httptest.NewServer(apiHandler(testAPI(t), discardLogger))
	defer srv.Close()

	photo := testimage.GradientJPEG(t, 300, 200)
	half := photo[:len(photo)/2]
	chunk := fmt.Sprintf("Transfer-Encoding: chunked\r\n\r\n%x\r\n%s", len(half), half)

	const malformed = "the request body is malformed"

	cases := []struct {
		name, request, message string
	}{
		{
			name:    "cut short",
			request: fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(photo), half),
			message: "the request body ended early",
		},
		{name: "bad chunk size", request: chunk + "\r\nzz\r\n", message: malformed},
		{name: "chunk without its CRLF", request: chunk + "XX", message: malformed},
		{name: "chunk length too large", request: chunk + "\r\nfffffffffffffffffff\r\n", message: malformed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})

			if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}

			fmt.Fprintf(conn, "POST /hash HTTP/1.1\r\nHost: blurb\r\n%s", tc.request)
			if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), `"code":"bad_request"`) ||
				!strings.Contains(string(b), tc.message) {
				t.Errorf("got %d %s, want 400 bad_request saying %q", resp.StatusCode, b, tc.message)
			}
		})
	}
}

// FuzzHashPost requires each answer to POST /hash to be JSON and none to be a
// 500, which is the status of a recovered panic and of an error [classify] has
// no case for.
func FuzzHashPost(f *testing.F) {
	jpegBytes := testimage.GradientJPEG(f, 30, 20)

	f.Add("", jpegBytes)
	f.Add("formats=blurhash,thumbhash&extras=dominant_colors,phash,lqip,sha256", jpegBytes)
	f.Add("formats=thumbhash&extras=lqip", testimage.WebPAnimated())
	f.Add("extras=sha256", testimage.GIFAnimated())
	f.Add("formats=&formats=blurhash", testimage.WebPLossyAlpha())
	f.Add("extras=%zz", []byte("not an image"))

	hashAPI := testAPI(f)
	h, err := NewHandler(
		testConfig(),
		hashAPI.fetcher,
		hashAPI.hashQueue,
		hashAPI.hasher.decodeQueue,
		testReady(),
		discardLogger,
		noop.NewMeterProvider(),
	)
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, query string, body []byte) {
		r := httptest.NewRequest(http.MethodPost, "/hash", bytes.NewReader(body))
		r.URL.RawQuery = query

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if rec.Code == http.StatusInternalServerError || !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("query %q, %d-byte body: got %d %s", query, len(body), rec.Code, rec.Body.String())
		}
	})
}

// TestClientBodyRead requires a read error to also match errBodyTimeout for a
// passed deadline, [context.Canceled] for another network error, errBodyCut
// when the body ended early and errBodyMalformed otherwise, except an
// [*http.MaxBytesError], which matches none of them.
func TestClientBodyRead(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		class error
	}{
		{name: "cut", err: io.ErrUnexpectedEOF, class: errBodyCut},
		{name: "framing", err: errors.New("invalid byte in chunk length"), class: errBodyMalformed},
		{name: "network", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, class: context.Canceled},
		{
			name:  "deadline",
			err:   &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded},
			class: errBodyTimeout,
		},
		{name: "too large", err: &http.MaxBytesError{Limit: 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&clientBody{r: iotest.ErrReader(tc.err)}).Read(make([]byte, 1))
			if !errors.Is(err, tc.err) {
				t.Fatalf("got %v, want it to wrap %v", err, tc.err)
			}

			for _, class := range []error{errBodyTimeout, context.Canceled, errBodyCut, errBodyMalformed} {
				if want := class == tc.class; errors.Is(err, class) != want {
					t.Errorf("got %v; matching %v is %t, want %t", err, class, !want, want)
				}
			}
		})
	}
}

// TestClientBodyDeadline requires a body that stalls, inside its header or
// after it, or that arrives a byte at a time, to be answered 408 once its
// deadline passes, and a complete request to be served after it.
func TestClientBodyDeadline(t *testing.T) {
	photo := testimage.GradientJPEG(t, 300, 200)

	cases := []struct {
		name    string
		sent    int
		trickle bool // the rest follows a byte every quarter deadline
	}{
		{name: "inside the header", sent: 1},
		{name: "after the header", sent: len(photo) / 2},
		{name: "a byte at a time", sent: 1, trickle: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decodeQueue, err := queue.New(1, queue.Options{})
			if err != nil {
				t.Fatal(err)
			}

			hashAPI := testAPI(t)
			hashAPI.bodyTimeout = testBodyTimeout
			hashAPI.hasher.decodeQueue = decodeQueue

			// Closed after the test's connections, since Close waits for
			// their requests.
			srv := httptest.NewServer(apiHandler(hashAPI, discardLogger))
			t.Cleanup(srv.Close)

			start := time.Now()
			stalled := sendPart(t, srv.Listener.Addr().String(), photo, tc.sent)

			if tc.trickle {
				go func() {
					for _, b := range photo[tc.sent:] {
						time.Sleep(testBodyTimeout / 4)

						if _, err := stalled.Write([]byte{b}); err != nil {
							return
						}
					}
				}()
			}

			resp, err := http.ReadResponse(bufio.NewReader(stalled), nil)
			if err != nil {
				t.Fatal(err)
			}

			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusRequestTimeout || !strings.Contains(string(b), `"code":"body_timeout"`) {
				t.Fatalf("stalled body: got %d %s, want 408 body_timeout", resp.StatusCode, b)
			}

			if took := time.Since(start); took > 4*testBodyTimeout {
				t.Errorf("stalled body answered after %v, want about %v", took, testBodyTimeout)
			}

			resp, err = http.Post(srv.URL+"/hash", "image/jpeg", bytes.NewReader(photo))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("after the stalled body: got %d, want 200", resp.StatusCode)
			}
		})
	}
}

// TestClientBodyDeadlinePaused requires a wait for a decode slot longer than
// the body's deadline not to use up the deadline.
func TestClientBodyDeadlinePaused(t *testing.T) {
	decodeQueue, err := queue.New(1, queue.Options{MaxWaitingRequests: 1})
	if err != nil {
		t.Fatal(err)
	}

	hashAPI := testAPI(t)
	hashAPI.bodyTimeout = testBodyTimeout
	hashAPI.hasher.decodeQueue = decodeQueue

	// Closed after the test's connections, since Close waits for their
	// requests.
	srv := httptest.NewServer(apiHandler(hashAPI, discardLogger))
	t.Cleanup(srv.Close)

	release, err := decodeQueue.Acquire(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}

	photo := testimage.GradientJPEG(t, 300, 200)
	conn := sendPart(t, srv.Listener.Addr().String(), photo, len(photo)/2)

	time.Sleep(3 * testBodyTimeout)
	release()

	if _, err := conn.Write(photo[len(photo)/2:]); err != nil {
		t.Fatal(err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}

	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("got %d %s, want 200", resp.StatusCode, b)
	}
}

// testBodyTimeout is the body deadline of the tests that let it pass.
const testBodyTimeout = 200 * time.Millisecond

// sendPart opens a connection to addr and sends a POST /hash declaring the
// whole of img but only its first n bytes.
func sendPart(t *testing.T, addr string, img []byte, n int) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	fmt.Fprintf(conn, "POST /hash HTTP/1.1\r\nHost: blurb\r\nContent-Length: %d\r\n\r\n", len(img))
	if _, err := conn.Write(img[:n]); err != nil {
		t.Fatal(err)
	}

	return conn
}
