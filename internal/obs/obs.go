// Package obs sets up the meter provider that blurb's metrics are exported
// through.
package obs

import (
	"context"
	"fmt"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// exporterVar is the variable that names the metrics exporter, as per the
// OpenTelemetry environment variable specification.
const exporterVar = "OTEL_METRICS_EXPORTER"

// serviceName is the service.name of the metrics' resource.
const serviceName = "blurb"

// Setup returns a meter provider and the func that flushes and stops it.
// OTEL_METRICS_EXPORTER, read through lookup, picks the exporter: unset or
// "none" records nothing, "otlp" exports over OTLP/HTTP with the Go runtime's
// metrics, and any other value is an error. version is the service.version of
// the metrics' resource. The OTEL_EXPORTER_OTLP_ variables configure the
// exporter, and OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES override the
// resource's attributes, all read from the process environment.
func Setup(
	ctx context.Context,
	lookup func(string) (string, bool),
	version string,
) (meters metric.MeterProvider, shutdown func(context.Context) error, err error) {
	switch exporter, _ := lookup(exporterVar); exporter {
	case "", "none":
		return noop.NewMeterProvider(), func(context.Context) error { return nil }, nil
	case "otlp":
	default:
		return nil, nil, fmt.Errorf("%s must be otlp or none, got %q", exporterVar, exporter)
	}

	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(semconv.ServiceName(serviceName), semconv.ServiceVersion(version)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("metrics resource: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)

	if err := otelruntime.Start(otelruntime.WithMeterProvider(provider)); err != nil {
		return nil, nil, fmt.Errorf("runtime metrics: %w", err)
	}

	return provider, provider.Shutdown, nil
}
