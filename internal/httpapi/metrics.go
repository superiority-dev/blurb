package httpapi

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/blurb/internal/queue"
)

// meterName is the instrumentation scope of the metrics this package records.
const meterName = "github.com/superiority-dev/blurb/internal/httpapi"

// The queue an attrQueue value names.
const (
	queueHash   = "hash"
	queueDecode = "decode"
)

// The reasons an attrReason value gives for a refusal.
const (
	reasonBusy     = "busy"
	reasonTimeout  = "timeout"
	reasonTooLarge = "too_large"
	reasonCanceled = "canceled"
)

// The units of the instruments, as per UCUM and OpenTelemetry.
const (
	unitRequests = "{request}"
	unitBytes    = "By"
	unitSeconds  = "s"
)

// codeOK is the attrCode of a request that was hashed.
const codeOK = "ok"

const (
	attrQueue  = attribute.Key("blurb.queue")
	attrReason = attribute.Key("blurb.reason")
	attrFormat = attribute.Key("blurb.format")
	attrCode   = attribute.Key("blurb.code")
)

var (
	// waitBuckets are the decode wait histogram's bounds, in seconds.
	waitBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10}

	// estimateBuckets are the estimate histogram's bounds, in bytes.
	estimateBuckets = []float64{1 << 20, 4 << 20, 16 << 20, 64 << 20, 256 << 20, 1 << 30}
)

// metrics holds the instruments the handlers record to.
type metrics struct {
	refused  metric.Int64Counter
	hashed   metric.Int64Counter
	wait     metric.Float64Histogram
	estimate metric.Int64Histogram
}

// newMetrics creates the instruments on meter, with the queues' counts
// observed at each collection.
func newMetrics(meter metric.Meter, hashQueue, decodeQueue *queue.Queue) (*metrics, error) {
	refused, err := meter.Int64Counter(
		"blurb.refused",
		metric.WithDescription("Requests refused by a queue."),
		metric.WithUnit(unitRequests),
	)
	if err != nil {
		return nil, err
	}

	hashed, err := meter.Int64Counter(
		"blurb.hash.requests",
		metric.WithDescription("Requests that reached the image read, by format and outcome."),
		metric.WithUnit(unitRequests),
	)
	if err != nil {
		return nil, err
	}

	wait, err := meter.Float64Histogram(
		"blurb.decode.wait.duration",
		metric.WithDescription("Time a request waited for its decode slot."),
		metric.WithUnit(unitSeconds),
		metric.WithExplicitBucketBoundaries(waitBuckets...),
	)
	if err != nil {
		return nil, err
	}

	estimate, err := meter.Int64Histogram(
		"blurb.decode.memory.estimate",
		metric.WithDescription("Memory a decode was estimated to need."),
		metric.WithUnit(unitBytes),
		metric.WithExplicitBucketBoundaries(estimateBuckets...),
	)
	if err != nil {
		return nil, err
	}

	hashActive, err := meter.Int64ObservableUpDownCounter(
		"blurb.hash.active",
		metric.WithDescription("Requests holding a hash queue place."),
		metric.WithUnit(unitRequests),
	)
	if err != nil {
		return nil, err
	}

	decodeActive, err := meter.Int64ObservableUpDownCounter(
		"blurb.decode.active",
		metric.WithDescription("Decodes running."),
		metric.WithUnit(unitRequests),
	)
	if err != nil {
		return nil, err
	}

	decodeWaiting, err := meter.Int64ObservableUpDownCounter(
		"blurb.decode.waiting",
		metric.WithDescription("Requests waiting for a decode slot."),
		metric.WithUnit(unitRequests),
	)
	if err != nil {
		return nil, err
	}

	reserved, err := meter.Int64ObservableUpDownCounter(
		"blurb.decode.memory.reserved",
		metric.WithDescription("Memory the running decodes are estimated to need."),
		metric.WithUnit(unitBytes),
	)
	if err != nil {
		return nil, err
	}

	memoryLimit, err := meter.Int64ObservableGauge(
		"blurb.decode.memory.limit",
		metric.WithDescription("Memory the running decodes may be estimated to need together, 0 for no bound."),
		metric.WithUnit(unitBytes),
	)
	if err != nil {
		return nil, err
	}

	_, err = meter.RegisterCallback(
		func(_ context.Context, observer metric.Observer) error {
			hash, decode := hashQueue.Stats(), decodeQueue.Stats()

			observer.ObserveInt64(hashActive, int64(hash.Running))
			observer.ObserveInt64(decodeActive, int64(decode.Running))
			observer.ObserveInt64(decodeWaiting, int64(decode.Waiting))
			observer.ObserveInt64(reserved, decode.Bytes)
			observer.ObserveInt64(memoryLimit, decode.MaxBytes)

			return nil
		},
		hashActive, decodeActive, decodeWaiting, reserved, memoryLimit,
	)
	if err != nil {
		return nil, err
	}

	return &metrics{refused: refused, hashed: hashed, wait: wait, estimate: estimate}, nil
}

// refuse counts a refusal by the queue named, for the error its Acquire
// returned.
func (m *metrics) refuse(ctx context.Context, queueName string, err error) {
	reason := reasonCanceled

	switch {
	case errors.Is(err, queue.ErrBusy):
		reason = reasonBusy
	case errors.Is(err, queue.ErrTimeout):
		reason = reasonTimeout
	case errors.Is(err, queue.ErrTooLarge):
		reason = reasonTooLarge
	}

	m.refused.Add(ctx, 1, metric.WithAttributes(attrQueue.String(queueName), attrReason.String(reason)))
}

// result counts a request that reached the image read, by its format and the
// code its error maps to. The format is empty when the header was unreadable.
func (m *metrics) result(ctx context.Context, format header.Format, err error) {
	code := codeOK
	if err != nil {
		_, code, _ = classify(err)
	}

	m.hashed.Add(ctx, 1, metric.WithAttributes(attrFormat.String(string(format)), attrCode.String(code)))
}
