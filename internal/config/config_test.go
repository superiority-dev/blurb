package config

import (
	"log/slog"
	"math"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env("BLURB_ALLOWED_HOSTS", "images.example.com", "BLURB_ADDR", "  "))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":8080" ||
		cfg.PathPrefix != "" ||
		cfg.MaxAge != 8760*time.Hour ||
		!cfg.FollowRedirects ||
		cfg.AllowPrivateAddresses ||
		cfg.FetchTimeout != 5*time.Second ||
		cfg.MaxDownloadBytes != 128<<20 ||
		cfg.MaxPixels != 50_000_000 ||
		cfg.MaxConcurrentDecodes != 0 ||
		cfg.MaxWaitingRequests != -1 ||
		cfg.QueueTimeout != 5*time.Second ||
		cfg.LogLevel != slog.LevelInfo ||
		cfg.ShutdownTimeout != 25*time.Second ||
		cfg.BaseURL != nil ||
		cfg.XComponents != 4 ||
		cfg.YComponents != 3 {
		t.Errorf("defaults: %+v", cfg)
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := Load(env(
		"BLURB_ADDR", " 127.0.0.1:1 ",
		"BLURB_PATH_PREFIX", "/AZaz09/v1.2_~-",
		"BLURB_ALLOWED_HOSTS", " a.example , b.example ",
		"BLURB_BASE_URL", "https://cdn.example/images/",
		"BLURB_FOLLOW_REDIRECTS", "false",
		"BLURB_ALLOW_PRIVATE_ADDRESSES", "true",
		"BLURB_FETCH_TIMEOUT", "1500ms",
		"BLURB_MAX_DOWNLOAD_BYTES", "64 MiB",
		"BLURB_MAX_WAITING_REQUESTS", "0",
		"BLURB_QUEUE_TIMEOUT", "2s",
		"BLURB_MAX_CONCURRENT_DECODES", "4096",
		"BLURB_LOG_LEVEL", "debug",
		"BLURB_SHUTDOWN_TIMEOUT", "1s",
		"BLURB_BLURHASH_COMPONENTS", "9x1",
	))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != "127.0.0.1:1" ||
		cfg.PathPrefix != "/AZaz09/v1.2_~-" ||
		strings.Join(cfg.AllowedHosts, "|") != "a.example|b.example" ||
		cfg.BaseURL.String() != "https://cdn.example/images/" ||
		cfg.FollowRedirects ||
		!cfg.AllowPrivateAddresses ||
		cfg.FetchTimeout != 1500*time.Millisecond ||
		cfg.MaxDownloadBytes != 64<<20 ||
		cfg.MaxWaitingRequests != 0 ||
		cfg.QueueTimeout != 2*time.Second ||
		cfg.MaxConcurrentDecodes != 4096 ||
		cfg.LogLevel != slog.LevelDebug ||
		cfg.ShutdownTimeout != time.Second ||
		cfg.XComponents != 9 ||
		cfg.YComponents != 1 {
		t.Errorf("values: %+v", cfg)
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
			_, err := Load(env("BLURB_ALLOWED_HOSTS", "a.example", "BLURB_"+tc.name, tc.value))
			if err == nil ||
				!strings.Contains(err.Error(), "BLURB_"+tc.name) ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error naming BLURB_%s and saying %q", err, tc.name, tc.want)
			}
		})
	}

	cfg, err := Load(env("BLURB_FETCH_TIMEOUT", "x", "BLURB_LOG_LEVEL", "x"))
	if err == nil ||
		strings.Count(err.Error(), "\n") != 2 ||
		!strings.Contains(err.Error(), "BLURB_ALLOWED_HOSTS is required") {
		t.Errorf("got %v, want three problems reported together, BLURB_ALLOWED_HOSTS required among them", err)
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
		cfg, err := Load(env("BLURB_ALLOWED_HOSTS", "a.example", "BLURB_MAX_MEGAPIXELS", tc.value))
		if err != nil || cfg.MaxPixels != tc.want {
			t.Errorf("%s: got %d, %v; want %d", tc.value, cfg.MaxPixels, err, tc.want)
		}
	}
}

// TestLoadDownloadBytesLimit requires MAX_DOWNLOAD_BYTES to accept 2^62
// bytes, in GiB too.
func TestLoadDownloadBytesLimit(t *testing.T) {
	cfg, err := Load(env("BLURB_ALLOWED_HOSTS", "a.example", "BLURB_MAX_DOWNLOAD_BYTES", "4294967296GiB"))
	if err != nil || cfg.MaxDownloadBytes != 1<<62 {
		t.Errorf("got %d, %v; want 2^62", cfg.MaxDownloadBytes, err)
	}
}

// TestLoadMaxAgeZero requires a MAX_AGE of 0, which sends no-store, to
// be accepted.
func TestLoadMaxAgeZero(t *testing.T) {
	cfg, err := Load(env("BLURB_ALLOWED_HOSTS", "a.example", "BLURB_MAX_AGE", "0s"))
	if err != nil || cfg.MaxAge != 0 {
		t.Errorf("got %v, %v; want 0 and no error", cfg.MaxAge, err)
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
		in     string
		want   int64
		wantOK bool
	}{
		{in: "4611686018427387903\n", want: 4611686018427387903, wantOK: true},
		{in: "4611686018427387904\n", want: 0, wantOK: false},
		{in: "0\n", want: 0, wantOK: false},
	}

	for _, tc := range cases {
		got, ok := parseCgroupLimit(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseCgroupLimit(%q) = %d, %t; want %d, %t", tc.in, got, ok, tc.want, tc.wantOK)
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

func env(kv ...string) func(string) (string, bool) {
	env := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		env[kv[i]] = kv[i+1]
	}

	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}
