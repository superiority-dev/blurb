package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/superiority-dev/blurb/internal/testenv"
)

// TestSetup requires no exporter to record nothing, an unknown exporter to be
// refused, and otlp, when it shuts down, to send the runtime's metrics to the
// OTLP endpoint with the version given and the service name blurb, or
// OTEL_SERVICE_NAME's when it is set.
func TestSetup(t *testing.T) {
	for _, exporter := range []string{"", "none"} {
		meters, shutdown, err := Setup(t.Context(), testenv.Lookup(exporterVar, exporter), "v1.2.3")
		if err != nil {
			t.Fatalf("%q: %v", exporter, err)
		}

		if _, ok := meters.(noop.MeterProvider); !ok {
			t.Errorf("%q: got %T, want a no-op provider", exporter, meters)
		}

		if err := shutdown(t.Context()); err != nil {
			t.Errorf("%q: shutdown: %v", exporter, err)
		}
	}

	if _, _, err := Setup(t.Context(), testenv.Lookup(exporterVar, "prometheus"), "v1.2.3"); err == nil {
		t.Error("prometheus accepted")
	}

	bodies := make(chan []byte, 4)
	collector := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/metrics" {
			b, _ := io.ReadAll(r.Body)
			bodies <- b
		}
	}))
	defer collector.Close()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	for _, tc := range []struct{ serviceEnv, service string }{
		{serviceEnv: "", service: serviceName},
		{serviceEnv: "thumbs", service: "thumbs"},
	} {
		t.Setenv("OTEL_SERVICE_NAME", tc.serviceEnv)

		_, shutdown, err := Setup(t.Context(), testenv.Lookup(exporterVar, "otlp"), "v1.2.3")
		if err != nil {
			t.Fatal(err)
		}

		if err := shutdown(context.WithoutCancel(t.Context())); err != nil {
			t.Fatalf("shutdown: %v", err)
		}

		if len(bodies) == 0 {
			t.Fatal("nothing reached the collector")
		}

		body := <-bodies
		for _, want := range []string{tc.service, "v1.2.3", "go.memory.used"} {
			if !bytes.Contains(body, []byte(want)) {
				t.Errorf("OTEL_SERVICE_NAME %q: export does not hold %q", tc.serviceEnv, want)
			}
		}
	}
}
