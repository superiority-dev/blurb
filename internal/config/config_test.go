package config

import (
	"log/slog"
	"math"
	"net/url"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/superiority-dev/blurb/internal/testenv"
)

// TestLoadValues requires each unset variable to give its default, and each
// set one its value, surrounding spaces trimmed.
func TestLoadValues(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want Config
	}{
		{
			name: "defaults",
			env:  []string{"BLURB_ALLOWED_HOSTS", "images.example.com", "BLURB_ADDR", "  "},
			want: Config{
				Addr:                  ":8080",
				PathPrefix:            "",
				MaxAge:                8760 * time.Hour,
				RequestIDHeader:       "X-Request-Id",
				AllowedHosts:          []string{"images.example.com"},
				BaseURL:               nil,
				FollowRedirects:       true,
				AllowPrivateAddresses: false,
				FetchTimeout:          5 * time.Second,
				MaxDownloadBytes:      128 << 20,
				MaxPixels:             50_000_000,
				MaxWaitingRequests:    -1,
				QueueTimeout:          5 * time.Second,
				MaxConcurrentDecodes:  0,
				XComponents:           4,
				YComponents:           3,
				LogLevel:              slog.LevelInfo,
				ShutdownTimeout:       25 * time.Second,
			},
		},
		{
			name: "every variable set",
			env: []string{
				"BLURB_ADDR", " 127.0.0.1:1 ",
				"BLURB_PATH_PREFIX", "/AZaz09/v1.2_~-",
				"BLURB_MAX_AGE", "0s",
				"BLURB_REQUEST_ID_HEADER", "AZaz09!#$%&'*+-.^_`|~",
				"BLURB_ALLOWED_HOSTS", " a.example , b.example ",
				"BLURB_BASE_URL", "https://cdn.example/images/",
				"BLURB_FOLLOW_REDIRECTS", "false",
				"BLURB_ALLOW_PRIVATE_ADDRESSES", "true",
				"BLURB_FETCH_TIMEOUT", "1500ms",
				"BLURB_MAX_DOWNLOAD_BYTES", "64 MiB",
				"BLURB_MAX_MEGAPIXELS", "12.5",
				"BLURB_MAX_WAITING_REQUESTS", "0",
				"BLURB_QUEUE_TIMEOUT", "2s",
				"BLURB_MAX_CONCURRENT_DECODES", "4096",
				"BLURB_BLURHASH_COMPONENTS", "9x1",
				"BLURB_LOG_LEVEL", "debug",
				"BLURB_SHUTDOWN_TIMEOUT", "1s",
			},
			want: Config{
				Addr:                  "127.0.0.1:1",
				PathPrefix:            "/AZaz09/v1.2_~-",
				MaxAge:                0,
				RequestIDHeader:       "AZaz09!#$%&'*+-.^_`|~",
				AllowedHosts:          []string{"a.example", "b.example"},
				BaseURL:               &url.URL{Scheme: "https", Host: "cdn.example", Path: "/images/"},
				FollowRedirects:       false,
				AllowPrivateAddresses: true,
				FetchTimeout:          1500 * time.Millisecond,
				MaxDownloadBytes:      64 << 20,
				MaxPixels:             12_500_000,
				MaxWaitingRequests:    0,
				QueueTimeout:          2 * time.Second,
				MaxConcurrentDecodes:  4096,
				XComponents:           9,
				YComponents:           1,
				LogLevel:              slog.LevelDebug,
				ShutdownTimeout:       time.Second,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(testenv.Lookup(tc.env...))
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(cfg, tc.want) {
				t.Errorf("got %+v, want %+v", cfg, tc.want)
			}
		})
	}
}

// TestLoadErrors requires every bad value to fail, naming its variable, and
// all of them to be reported together.
func TestLoadErrors(t *testing.T) {
	cases := []struct{ name, value, want string }{
		{name: "PATH_PREFIX", value: "v1", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/v1/", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "//v1", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/v1/..", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/./v1", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/v1?x=1", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/v1#x", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/{v}", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/v 1", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/%76", want: "a path such as /v1"},
		{name: "PATH_PREFIX", value: "/vé", want: "a path such as /v1"},
		{name: "MAX_AGE", value: "-1s", want: "whole seconds from 0s to 8760h0m0s"},
		{name: "MAX_AGE", value: "1500ms", want: "whole seconds from 0s to 8760h0m0s"},
		{name: "MAX_AGE", value: "8760h1s", want: "whole seconds from 0s to 8760h0m0s"},
		{name: "REQUEST_ID_HEADER", value: "X Request Id", want: "a header name"},
		{name: "REQUEST_ID_HEADER", value: "X-Request-Id:", want: "a header name"},
		{name: "REQUEST_ID_HEADER", value: "X-Réquest-Id", want: "a header name"},
		{name: "ALLOWED_HOSTS", value: "a.example,,b.example", want: "empty entry"},
		{name: "BASE_URL", value: "cdn.example/images", want: "absolute http or https URL"},
		{name: "BASE_URL", value: "https://user:pass@cdn.example/", want: "without credentials"},
		{name: "BASE_URL", value: "https://cdn.example/%zz", want: "absolute http or https URL"},
		{name: "FOLLOW_REDIRECTS", value: "yes", want: "true or false"},
		{name: "FETCH_TIMEOUT", value: "5", want: "duration"},
		{name: "FETCH_TIMEOUT", value: "0s", want: "more than zero"},
		{name: "MAX_DOWNLOAD_BYTES", value: "128MB", want: "byte count"},
		{name: "MAX_DOWNLOAD_BYTES", value: "-1", want: "byte count"},
		{name: "MAX_DOWNLOAD_BYTES", value: "0", want: "more than zero"},
		{name: "MAX_DOWNLOAD_BYTES", value: "9223372036854775807GiB", want: "byte count"},
		{name: "MAX_DOWNLOAD_BYTES", value: "4294967297GiB", want: "at most 4611686018427387904"},
		{name: "MAX_MEGAPIXELS", value: "0", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: "1000.000001", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: "1.0000001", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: "1.", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: ".5", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: "+1", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: "1.+5", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: "12,5", want: "megapixels such as 50"},
		{name: "MAX_MEGAPIXELS", value: "76480200929599801", want: "megapixels such as 50"},
		{name: "MAX_CONCURRENT_DECODES", value: "4097", want: "between 0 and 4096"},
		{name: "MAX_WAITING_REQUESTS", value: "-1", want: "between 0 and 65536"},
		{name: "MAX_WAITING_REQUESTS", value: "four", want: "whole number"},
		{name: "QUEUE_TIMEOUT", value: "0s", want: "more than zero"},
		{name: "LOG_LEVEL", value: "verbose", want: "debug, info, warn or error"},
		{name: "SHUTDOWN_TIMEOUT", value: "-1s", want: "more than zero"},
		{name: "BLURHASH_COMPONENTS", value: "4", want: "written XxY"},
		{name: "BLURHASH_COMPONENTS", value: "0x3", want: "written XxY"},
		{name: "BLURHASH_COMPONENTS", value: "4x10", want: "written XxY"},
		{name: "BLURHASH_COMPONENTS", value: "10x3", want: "written XxY"},
		{name: "BLURHASH_COMPONENTS", value: "4x0", want: "written XxY"},
	}

	for _, tc := range cases {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			_, err := Load(testenv.Lookup("BLURB_ALLOWED_HOSTS", "a.example", "BLURB_"+tc.name, tc.value))
			if err == nil {
				t.Fatalf("got no error, want one naming BLURB_%s", tc.name)
			}

			if !strings.Contains(err.Error(), "BLURB_"+tc.name) {
				t.Errorf("got %v, want it to name BLURB_%s", err, tc.name)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want it to say %q", err, tc.want)
			}
		})
	}

	cfg, err := Load(testenv.Lookup("BLURB_FETCH_TIMEOUT", "x", "BLURB_LOG_LEVEL", "x"))
	if err == nil {
		t.Fatal("got no error, want three problems reported together")
	}

	if n := strings.Count(err.Error(), "\n") + 1; n != 3 {
		t.Errorf("got %d problems, want 3: %v", n, err)
	}

	if !strings.Contains(err.Error(), "BLURB_ALLOWED_HOSTS is required") {
		t.Errorf("got %v, want BLURB_ALLOWED_HOSTS required among the problems", err)
	}

	if !reflect.ValueOf(cfg).IsZero() {
		t.Errorf("got %+v with the error, want the zero Config", cfg)
	}
}

// TestLoadMegapixels requires MAX_MEGAPIXELS to give the exact pixel count,
// at both ends of its range.
func TestLoadMegapixels(t *testing.T) {
	cases := []struct {
		value string
		want  int64
	}{
		{value: "0.000001", want: 1},
		{value: "0.000249", want: 249},
		{value: "12.5", want: 12_500_000},
		{value: "1000", want: 1_000_000_000},
	}

	for _, tc := range cases {
		cfg, err := Load(testenv.Lookup("BLURB_ALLOWED_HOSTS", "a.example", "BLURB_MAX_MEGAPIXELS", tc.value))
		if err != nil || cfg.MaxPixels != tc.want {
			t.Errorf("%s: got %d, %v; want %d", tc.value, cfg.MaxPixels, err, tc.want)
		}
	}
}

// TestLoadDownloadBytes requires MAX_DOWNLOAD_BYTES to give the exact byte
// count in each unit or none, up to 2^62.
func TestLoadDownloadBytes(t *testing.T) {
	cases := []struct {
		value string
		want  int64
	}{
		{value: "134217728", want: 128 << 20},
		{value: "1B", want: 1},
		{value: "1KiB", want: 1 << 10},
		{value: "1MiB", want: 1 << 20},
		{value: "4294967296GiB", want: 1 << 62},
	}

	for _, tc := range cases {
		cfg, err := Load(testenv.Lookup("BLURB_ALLOWED_HOSTS", "a.example", "BLURB_MAX_DOWNLOAD_BYTES", tc.value))
		if err != nil || cfg.MaxDownloadBytes != tc.want {
			t.Errorf("%s: got %d, %v; want %d", tc.value, cfg.MaxDownloadBytes, err, tc.want)
		}
	}
}

func TestDecodeLimits(t *testing.T) {
	cases := []struct {
		limit      int64
		concurrent int
		waiting    int
		procs      int
		want       DecodeLimits
	}{
		{
			limit:      1023,
			concurrent: 3,
			waiting:    -1,
			procs:      8,
			want: DecodeLimits{
				MaxMemoryBytes:       768,
				MaxConcurrentDecodes: 3,
				MaxWaitingRequests:   12,
			},
		},
		{
			limit:      1000,
			concurrent: 3,
			waiting:    0,
			procs:      8,
			want: DecodeLimits{
				MaxMemoryBytes:       750,
				MaxConcurrentDecodes: 3,
				MaxWaitingRequests:   0,
			},
		},
		{
			limit:      1000,
			concurrent: 3,
			waiting:    5,
			procs:      8,
			want: DecodeLimits{
				MaxMemoryBytes:       750,
				MaxConcurrentDecodes: 3,
				MaxWaitingRequests:   5,
			},
		},
		{
			limit:      math.MaxInt64 - 1,
			concurrent: 0,
			waiting:    -1,
			procs:      1,
			want: DecodeLimits{
				MaxMemoryBytes:       6917529027641081855,
				MaxConcurrentDecodes: 1,
				MaxWaitingRequests:   4,
			},
		},
	}

	for _, tc := range cases {
		cfg := Config{MaxConcurrentDecodes: tc.concurrent, MaxWaitingRequests: tc.waiting}
		if got := cfg.DecodeLimits(tc.limit, tc.procs); got != tc.want {
			t.Errorf(
				"DecodeLimits(%d, %d) with MAX_CONCURRENT_DECODES %d, MAX_WAITING_REQUESTS %d = %+v, want %+v",
				tc.limit,
				tc.procs,
				tc.concurrent,
				tc.waiting,
				got,
				tc.want,
			)
		}
	}
}

// TestMemoryLimit requires a limit set as GOMEMLIMIT sets it to be reported
// with the source "GOMEMLIMIT".
func TestMemoryLimit(t *testing.T) {
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(1 << 30))

	if limit, source := MemoryLimit(); limit != 1<<30 || source != "GOMEMLIMIT" {
		t.Errorf("got %d from %q, want %d from GOMEMLIMIT", limit, source, 1<<30)
	}
}

func TestParseCgroupLimit(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{in: "4611686018427387903\n", want: 4611686018427387903},
		{in: "4611686018427387904\n", want: 0},
		{in: "0\n", want: 0},
	}

	for _, tc := range cases {
		got, ok := parseCgroupLimit(tc.in)
		if got != tc.want || ok != (tc.want > 0) {
			t.Errorf("parseCgroupLimit(%q) = %d, %t; want %d, %t", tc.in, got, ok, tc.want, tc.want > 0)
		}
	}
}

func TestCgroupMemoryLimit(t *testing.T) {
	cases := []struct {
		name    string
		fsys    fstest.MapFS
		want    int64
		wantSrc string
	}{
		{name: "nested scope", fsys: fstest.MapFS{
			"proc/self/cgroup": {Data: []byte("0::/user.slice/app.scope\n")},
			"sys/fs/cgroup/user.slice/app.scope/memory.max": {Data: []byte("314572800\n")},
			"sys/fs/cgroup/user.slice/memory.max":           {Data: []byte("1073741824\n")},
		}, want: 314572800, wantSrc: "/sys/fs/cgroup/user.slice/app.scope/memory.max"},
		{name: "smaller parent", fsys: fstest.MapFS{
			"proc/self/cgroup":             {Data: []byte("0::/a/b\n")},
			"sys/fs/cgroup/a/b/memory.max": {Data: []byte("314572800\n")},
			"sys/fs/cgroup/a/memory.max":   {Data: []byte("200000000\n")},
		}, want: 200000000, wantSrc: "/sys/fs/cgroup/a/memory.max"},
		{name: "v1, memory comounted, host path not mounted", fsys: fstest.MapFS{
			"proc/self/cgroup": {
				Data: []byte("5:cpu,cpuacct:/docker/abc\n4:memory,devices:/docker/abc\n"),
			},
			"sys/fs/cgroup/memory/memory.limit_in_bytes": {
				Data: []byte("268435456\n"),
			},
		}, want: 268435456, wantSrc: "/sys/fs/cgroup/memory/memory.limit_in_bytes"},
		{name: "no limit", fsys: fstest.MapFS{
			"proc/self/cgroup":         {Data: []byte("0::/\n")},
			"sys/fs/cgroup/memory.max": {Data: []byte("max\n")},
		}, want: 0, wantSrc: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, src, ok := cgroupMemoryLimit(tc.fsys)
			if got != tc.want || src != tc.wantSrc || ok != (tc.want > 0) {
				t.Errorf("got %d from %q, %t; want %d from %q", got, src, ok, tc.want, tc.wantSrc)
			}
		})
	}
}
