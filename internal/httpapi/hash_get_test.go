package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/queue"
	"github.com/superiority-dev/blurb/internal/testimage"
	"github.com/superiority-dev/blurhash"
)

func TestHashGet(t *testing.T) {
	origin := newOrigin(t)
	h := apiHandler(testAPI(t), discardLogger)

	rec := serve(h, http.MethodGet, "/hash?url="+url.QueryEscape(origin.URL+"/photo.jpg"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200", rec.Code, rec.Body.String())
	}

	for name, want := range map[string]string{
		"Content-Type":           "application/json",
		"Cache-Control":          "public, max-age=3600, immutable",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s %q, want %q", name, got, want)
		}
	}

	var got struct {
		BlurHash, ThumbHash, Format, Color string
		Width, Height, Orientation         int
		Aspect                             float64
		Bytes                              int64
		HasAlpha                           bool `json:"has_alpha"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	th, err := base64.StdEncoding.DecodeString(got.ThumbHash)
	if err != nil || len(th) == 0 || got.Format != "jpeg" || got.Width != 300 || got.Height != 200 {
		t.Errorf("body %s", rec.Body.String())
	}

	if xComponents, yComponents, err := blurhash.Components(got.BlurHash); err != nil ||
		xComponents != 4 || yComponents != 3 {
		t.Errorf("BlurHash %q has %dx%d components, %v; want 4x3", got.BlurHash, xComponents, yComponents, err)
	}

	if got.Aspect != 1.5 ||
		got.Bytes != int64(len(testimage.GradientJPEG(t, 300, 200))) ||
		got.HasAlpha ||
		got.Orientation != 1 ||
		!hexColorPattern.MatchString(got.Color) {
		t.Errorf("metadata in %s", rec.Body.String())
	}

	for _, extra := range []string{"dominant_colors", "phash", "lqip", "sha256"} {
		if strings.Contains(rec.Body.String(), `"`+extra+`"`) {
			t.Errorf("%s present without being asked for: %s", extra, rec.Body.String())
		}
	}
}

// TestHashGetExtras requires each extra asked for to come back under its own
// key, and an unknown extra to answer 400.
func TestHashGetExtras(t *testing.T) {
	origin := newOrigin(t)
	h := apiHandler(testAPI(t), discardLogger)
	photo := url.QueryEscape(origin.URL + "/photo.jpg")

	rec := serve(h, http.MethodGet, "/hash?extras=dominant_colors,phash,lqip,sha256&url="+photo, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}

	var got struct {
		DominantColors []struct {
			Color string
			Share float64
		} `json:"dominant_colors"`
		PHash, LQIP, SHA256 string
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(testimage.GradientJPEG(t, 300, 200))
	if len(got.DominantColors) == 0 ||
		!hexColorPattern.MatchString(got.DominantColors[0].Color) ||
		got.DominantColors[0].Share <= 0 ||
		len(got.PHash) != 16 ||
		!strings.HasPrefix(got.LQIP, "data:image/png;base64,") ||
		got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("extras in %s", rec.Body.String())
	}

	if rec := serve(h, http.MethodGet, "/hash?extras=exif&url="+photo, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("extras=exif: %d, want 400", rec.Code)
	}
}

// TestHashGetFormats requires formats to choose the hashes and to default to
// both, and an unknown format to answer 400.
func TestHashGetFormats(t *testing.T) {
	origin := newOrigin(t)
	h := apiHandler(testAPI(t), discardLogger)
	photo := url.QueryEscape(origin.URL + "/photo.jpg")

	cases := []struct {
		formats         string
		status          int
		blurhash, thumb bool
	}{
		{formats: "", status: http.StatusOK, blurhash: true, thumb: true},
		{formats: "blurhash", status: http.StatusOK, blurhash: true, thumb: false},
		{formats: "thumbhash", status: http.StatusOK, blurhash: false, thumb: true},
		{formats: "blurhash,thumbhash", status: http.StatusOK, blurhash: true, thumb: true},
		{formats: "averagehash", status: http.StatusBadRequest, blurhash: false, thumb: false},
	}

	for _, tc := range cases {
		rec := serve(h, http.MethodGet, "/hash?url="+photo+"&formats="+tc.formats, nil)
		body := rec.Body.String()

		if rec.Code != tc.status ||
			strings.Contains(body, `"blurhash"`) != tc.blurhash ||
			strings.Contains(body, `"thumbhash"`) != tc.thumb {
			t.Errorf("formats=%q: %d %s", tc.formats, rec.Code, body)
		}
	}
}

// TestHashGetRepeated requires a 400 naming url, formats or extras when the
// query gives it twice.
func TestHashGetRepeated(t *testing.T) {
	h := apiHandler(testAPI(t), discardLogger)

	for _, name := range []string{"url", "formats", "extras"} {
		target := "/hash?url=https://a.example/x.jpg&formats=blurhash&extras=phash&" + name + "=x"
		rec := serve(h, http.MethodGet, target, nil)
		checkRepeated(t, rec, name)
	}
}

// TestHashGetQueueWait requires the wait for a decode slot not to count
// against the fetch timeout.
func TestHashGetQueueWait(t *testing.T) {
	origin := newOrigin(t)

	opts := fetch.Options{AllowedHosts: []string{"127.0.0.1"}, AllowPrivateAddresses: true}
	fetcher, err := fetch.New(time.Second, maxTestBody, opts)
	if err != nil {
		t.Fatal(err)
	}

	hashAPI := testAPI(t)
	hashAPI.fetcher = fetcher
	hashAPI.hasher.maxPixels = 1 << 21 // over large's 1100x1000

	hashAPI.hasher.decodeQueue, err = queue.New(1, queue.Options{MaxWaitingRequests: 1})
	if err != nil {
		t.Fatal(err)
	}
	h := apiHandler(hashAPI, discardLogger)

	large := testimage.GradientJPEG(t, 1100, 1000)
	held := len(large) - 1000
	pr, pw := io.Pipe()
	posted := make(chan int, 1)

	// Fail the writes if the POST stops reading.
	stalled := time.AfterFunc(10*time.Second, func() { pr.CloseWithError(errors.New("POST stopped reading")) })
	defer stalled.Stop()

	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hash", pr))
		posted <- rec.Code
	}()

	// The write returns once the decoder holding the slot has read it.
	if _, err := pw.Write(large[:held]); err != nil {
		t.Fatal(err)
	}

	fetched := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		fetched <- serve(h, http.MethodGet, "/hash?url="+url.QueryEscape(origin.URL+"/large.jpg"), nil)
	}()

	time.Sleep(2 * time.Second)

	if _, err := pw.Write(large[held:]); err != nil {
		t.Fatal(err)
	}

	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case rec := <-fetched:
		if rec.Code != http.StatusOK {
			t.Errorf("GET answered %d %s, want 200", rec.Code, rec.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GET did not answer within 10s")
	}

	if code := <-posted; code != http.StatusOK {
		t.Errorf("POST answered %d, want 200", code)
	}
}

// TestHashGetErrors requires an origin's error status, an origin hanging up,
// an image that fails to decode and a timeout during the decode to reach the
// client with their status and code.
func TestHashGetErrors(t *testing.T) {
	origin := newOrigin(t)
	h := apiHandler(testAPI(t), discardLogger)

	cases := []struct {
		target string
		status int
		code   string
	}{
		{target: origin.URL + "/missing.jpg", status: http.StatusNotFound, code: "upstream_not_found"},
		{target: origin.URL + "/hang-up", status: http.StatusBadGateway, code: "upstream_failed"},
		{target: origin.URL + "/truncated.jpg", status: http.StatusUnprocessableEntity, code: "unreadable_image"},
		{target: origin.URL + "/stall-mid-decode.jpg", status: http.StatusGatewayTimeout, code: "upstream_timeout"},
	}

	for _, tc := range cases {
		answered := make(chan *httptest.ResponseRecorder, 1)
		go func() { answered <- serve(h, http.MethodGet, "/hash?url="+url.QueryEscape(tc.target), nil) }()

		var rec *httptest.ResponseRecorder
		select {
		case rec = <-answered:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no answer after 5s", tc.target)
		}

		var body errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil ||
			rec.Code != tc.status ||
			body.Error.Code != tc.code {
			t.Errorf("%s: got %d %s, want %d %s", tc.target, rec.Code, rec.Body.String(), tc.status, tc.code)
		}
	}
}

// TestHashGetBytes requires the bytes field to be the origin's declared length,
// without waiting for the whole body, and the number of bytes read when the
// origin declares none.
func TestHashGetBytes(t *testing.T) {
	origin := newOrigin(t)
	h := apiHandler(testAPI(t), discardLogger)
	photo := testimage.GradientJPEG(t, 300, 200)

	for _, tc := range []struct {
		path  string
		bytes int
	}{
		{path: "/undeclared.jpg", bytes: len(photo) + originPad},
		{path: "/declared.jpg", bytes: len(photo) + 2*originPad},
	} {
		rec := serve(h, http.MethodGet, "/hash?url="+url.QueryEscape(origin.URL+tc.path), nil)
		want := fmt.Sprintf(`"bytes":%d,`, tc.bytes)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: got %d %s, want %s", tc.path, rec.Code, rec.Body.String(), want)
		}
	}
}

// originPad is what newOrigin's undeclared.jpg and declared.jpg send after
// the image.
const originPad = 64 << 10 // 64 KiB

// newOrigin returns a started server with the fixtures the handler tests fetch.
func newOrigin(t *testing.T) *httptest.Server {
	t.Helper()

	photo := testimage.GradientJPEG(t, 300, 200)
	large := testimage.GradientJPEG(t, 1100, 1000)
	release := make(chan struct{})

	mux := http.NewServeMux()
	for path, body := range map[string][]byte{
		"/photo.jpg":     photo,
		"/large.jpg":     large,
		"/truncated.jpg": photo[:len(photo)/2],
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, "%s", body) })
	}

	mux.HandleFunc("/hang-up", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	mux.HandleFunc("/stall-mid-decode.jpg", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s", photo[:len(photo)/2])
		w.(http.Flusher).Flush()
		<-release
	})
	pad := make([]byte, originPad)
	mux.HandleFunc("/undeclared.jpg", func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		fmt.Fprintf(w, "%s%s", photo, pad)
	})
	// declared.jpg declares originPad more bytes than it sends, then stalls.
	mux.HandleFunc("/declared.jpg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(photo)+2*len(pad)))
		fmt.Fprintf(w, "%s%s", photo, pad)
		w.(http.Flusher).Flush()
		<-release
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	return srv
}

var hexColorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)
