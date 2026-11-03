// Package config reads the service's settings from the environment and
// checks them before anything starts.
package config

import (
	"errors"
	"log/slog"
	"net/url"
	"time"
)

const defaultMaxAge = 365 * 24 * time.Hour

const (
	maxPixelsLimit            = 1_000_000_000 // MAX_MEGAPIXELS, in pixels
	maxConcurrentDecodesLimit = 4096          // MAX_CONCURRENT_DECODES
	maxWaitingRequestsLimit   = 1 << 16       // MAX_WAITING_REQUESTS, 65536
	maxAgeLimit               = defaultMaxAge // MAX_AGE
	maxBytesLimit             = 1 << 62       // MAX_DOWNLOAD_BYTES, 4 EiB
	maxComponents             = 9             // each axis of BLURHASH_COMPONENTS
)

const megapixel = 1_000_000

// unsetWaiting is [Config.MaxWaitingRequests] when MAX_WAITING_REQUESTS is
// not set.
const unsetWaiting = -1

// waitingPerSlot is how many requests may wait per decode slot when
// MAX_WAITING_REQUESTS is not set.
const waitingPerSlot = 4

// memoryLimitHeadroomPercent is the part of the memory limit
// [Config.DecodeLimits] keeps for everything but decodes.
const memoryLimitHeadroomPercent = 25

// Config holds every setting.
type Config struct {
	Addr string

	// PathPrefix, such as /v1, is prepended to /hash but not to the probes.
	PathPrefix string

	// MaxAge is the Cache-Control max-age sent with a hash of a fetched image;
	// 0 sends no-store.
	MaxAge time.Duration

	// RequestIDHeader names the header a request id is read from and sent
	// back in.
	RequestIDHeader string

	AllowedHosts []string

	// BaseURL resolves relative image URLs; nil means every URL must be
	// absolute.
	BaseURL *url.URL

	FollowRedirects       bool
	AllowPrivateAddresses bool

	// FetchTimeout is the time limit for each fetch, including connection
	// time, any redirects and the time its body's reads wait on the origin,
	// and for the reads of each POST body.
	FetchTimeout time.Duration

	MaxDownloadBytes int64
	MaxPixels        int64

	// MaxWaitingRequests of 0 means none wait, and -1 that it is not set.
	MaxWaitingRequests int

	// QueueTimeout bounds a wait for a decode slot; a request that waits
	// longer is answered 503.
	QueueTimeout time.Duration

	// MaxConcurrentDecodes of 0 means GOMAXPROCS.
	MaxConcurrentDecodes int

	// XComponents is the number of horizontal BlurHash components.
	XComponents int

	// YComponents is the number of vertical BlurHash components.
	YComponents int

	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// Load reads every setting through lookup, which has the signature of
// [os.LookupEnv], and returns all the problems it finds at once.
func Load(lookup func(string) (string, bool)) (Config, error) {
	env := reader{lookup: lookup}

	cfg := Config{
		Addr:                  env.str("ADDR", ":8080"),
		PathPrefix:            env.pathPrefix("PATH_PREFIX"),
		MaxAge:                env.seconds("MAX_AGE", defaultMaxAge, maxAgeLimit),
		RequestIDHeader:       env.headerName("REQUEST_ID_HEADER", "X-Request-Id"),
		AllowedHosts:          env.allowlist("ALLOWED_HOSTS"),
		BaseURL:               env.url("BASE_URL"),
		FollowRedirects:       env.bool("FOLLOW_REDIRECTS", true),
		AllowPrivateAddresses: env.bool("ALLOW_PRIVATE_ADDRESSES", false),
		FetchTimeout:          env.positiveDuration("FETCH_TIMEOUT", 5*time.Second),
		MaxDownloadBytes:      env.bytes("MAX_DOWNLOAD_BYTES", 128<<20),
		MaxPixels:             env.megapixels("MAX_MEGAPIXELS", 50*megapixel),
		MaxConcurrentDecodes:  int(env.count("MAX_CONCURRENT_DECODES", 0, maxConcurrentDecodesLimit)),
		MaxWaitingRequests:    int(env.count("MAX_WAITING_REQUESTS", unsetWaiting, maxWaitingRequestsLimit)),
		QueueTimeout:          env.positiveDuration("QUEUE_TIMEOUT", 5*time.Second),
		LogLevel:              env.level("LOG_LEVEL", slog.LevelInfo),
		ShutdownTimeout:       env.positiveDuration("SHUTDOWN_TIMEOUT", 25*time.Second),
	}

	cfg.XComponents, cfg.YComponents = env.components("BLURHASH_COMPONENTS", 4, 3)

	if err := errors.Join(env.errs...); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// DecodeLimits returns the [DecodeLimits] for a process whose memory limit is
// memoryLimit bytes, 0 for none, and whose GOMAXPROCS is gomaxprocs.
func (c Config) DecodeLimits(memoryLimit int64, gomaxprocs int) DecodeLimits {
	limits := DecodeLimits{
		MaxMemoryBytes:       lessPercent(memoryLimit, memoryLimitHeadroomPercent),
		MaxConcurrentDecodes: c.MaxConcurrentDecodes,
		MaxWaitingRequests:   c.MaxWaitingRequests,
	}
	limits.CollectAboveBytes = (memoryLimit - limits.MaxMemoryBytes) / 2

	if limits.MaxConcurrentDecodes == 0 {
		limits.MaxConcurrentDecodes = gomaxprocs
	}

	if limits.MaxWaitingRequests == unsetWaiting {
		limits.MaxWaitingRequests = waitingPerSlot * limits.MaxConcurrentDecodes
	}

	return limits
}

// DecodeLimits holds the memory the decodes may use together, how many may
// run at once and how many may wait.
type DecodeLimits struct {
	// MaxMemoryBytes of 0 means no bound.
	MaxMemoryBytes int64

	// CollectAboveBytes is half the part of the memory limit kept for
	// everything but decodes, 0 when there is no limit, so that decode
	// garbage takes at most half of that part.
	CollectAboveBytes int64

	MaxConcurrentDecodes int
	MaxWaitingRequests   int
}

// lessPercent returns n less the given percent of it, dividing by 100 first
// so n times percent cannot overflow.
func lessPercent(n, percent int64) int64 {
	return n - (n/100*percent + n%100*percent/100)
}
