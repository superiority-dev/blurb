package httpapi

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// docOK is the JSON document a passing probe answers.
const docOK = `{"status":"ok"}`

// Readiness reports whether the service takes traffic. The zero value is
// not ready.
type Readiness struct {
	ready atomic.Bool
}

// Set marks the service ready or not.
func (r *Readiness) Set(ready bool) { r.ready.Store(ready) }

func (r *Readiness) readyz(w http.ResponseWriter, _ *http.Request) {
	if !r.ready.Load() {
		writeError(w, http.StatusServiceUnavailable, codeNotReady, "the service is not ready")
		return
	}

	writeJSON(w, http.StatusOK, noStore, json.RawMessage(docOK))
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, noStore, json.RawMessage(docOK))
}
