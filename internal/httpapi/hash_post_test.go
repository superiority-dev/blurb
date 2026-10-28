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
	"image/color/palette"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

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

	checkLog(t, logs, map[string]any{"status": float64(http.StatusServiceUnavailable), "level": "INFO"})
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

// TestClientBodyRead requires a read error to also match [context.Canceled] for
// a network error, errBodyCut when the body ended early and errBodyMalformed
// otherwise, except an [*http.MaxBytesError], which matches none of them.
func TestClientBodyRead(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		class error
	}{
		{name: "cut", err: io.ErrUnexpectedEOF, class: errBodyCut},
		{name: "framing", err: errors.New("invalid byte in chunk length"), class: errBodyMalformed},
		{name: "network", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, class: context.Canceled},
		{name: "too large", err: &http.MaxBytesError{Limit: 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := clientBody{r: iotest.ErrReader(tc.err)}.Read(make([]byte, 1))
			if !errors.Is(err, tc.err) {
				t.Fatalf("got %v, want it to wrap %v", err, tc.err)
			}

			for _, class := range []error{context.Canceled, errBodyCut, errBodyMalformed} {
				if want := class == tc.class; errors.Is(err, class) != want {
					t.Errorf("got %v; matching %v is %t, want %t", err, class, !want, want)
				}
			}
		})
	}
}

// noiseGIF returns a GIF of random palette indices, larger than maxTestBody so
// a reader limited to it runs out in the middle of the pixels.
func noiseGIF(t *testing.T) []byte {
	t.Helper()

	img := image.NewPaletted(image.Rect(0, 0, 700, 700), palette.Plan9)
	rng := rand.New(rand.NewPCG(1, 2))

	for i := range img.Pix {
		img.Pix[i] = uint8(rng.IntN(len(palette.Plan9)))
	}

	data := testimage.EncodeGIF(t, img)
	if len(data) <= maxTestBody {
		t.Fatalf("noise GIF is %d bytes, not over %d", len(data), maxTestBody)
	}

	return data
}
