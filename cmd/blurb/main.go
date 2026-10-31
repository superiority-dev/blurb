// Command blurb serves placeholder hashes for images over HTTP.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"syscall"
	"time"

	"github.com/superiority-dev/blurb/internal/config"
	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/httpapi"
	"github.com/superiority-dev/blurb/internal/queue"
)

const readHeaderTimeout = 10 * time.Second

const idleTimeout = 2 * time.Minute

const versionFlag = "version"

const softMemoryLimitHeadroomPercent = 10

const unknown = "unknown"

// Build details, each set at link time with -ldflags "-X main.NAME=VALUE".
var (
	version = "dev"
	commit  = unknown
	date    = unknown
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// After the first signal a second one ends the process at once.
	go func() {
		<-ctx.Done()
		stop()
	}()

	err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout)
	stop()

	if err != nil {
		os.Exit(1)
	}
}

// run serves until ctx is done, then drains in-flight requests. It logs the
// error it returns.
func run(ctx context.Context, args []string, lookup func(string) (string, bool), stdout io.Writer) (err error) {
	level := new(slog.LevelVar)
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: level}))

	defer func() {
		if err != nil {
			logger.Error("exiting", slog.Any("error", err))
		}
	}()

	flags := flag.NewFlagSet("blurb", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	showVersion := flags.Bool(versionFlag, false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Fprintf(stdout, "blurb %s, commit %s, built %s\n", version, commit, date)
		return nil
	}

	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}

	level.Set(cfg.LogLevel)

	limit, limitSource := config.MemoryLimit()
	decodeLimits := cfg.DecodeLimits(limit, runtime.GOMAXPROCS(0))

	soft := debug.SetMemoryLimit(-1)
	if softToSet := softLimit(limit, limitSource); softToSet > 0 {
		soft = softToSet
		debug.SetMemoryLimit(soft)
	}

	queueOpts := queue.Options{
		MaxWaitingRequests: decodeLimits.MaxWaitingRequests,
		Timeout:            cfg.QueueTimeout,
		MaxMemoryBytes:     decodeLimits.MaxMemoryBytes,
	}

	logger.Info("starting",
		slog.String("version", version),
		slog.String("commit", commit),
		slog.Int64("memory_limit_bytes", limit),
		slog.String("memory_limit_source", limitSource),
		slog.Int64("soft_memory_limit_bytes", soft),
		slog.Int64("decode_memory_bytes", queueOpts.MaxMemoryBytes),
		slog.Int("max_concurrent_decodes", decodeLimits.MaxConcurrentDecodes),
		slog.Int("max_waiting_requests", queueOpts.MaxWaitingRequests),
		slog.Duration("queue_timeout", queueOpts.Timeout),
		slog.Int64("max_pixels", cfg.MaxPixels),
		slog.String("blurhash_components", fmt.Sprintf("%dx%d", cfg.XComponents, cfg.YComponents)),
	)

	decodeQueue, err := queue.New(decodeLimits.MaxConcurrentDecodes, queueOpts)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}

	fetcher, err := fetch.New(cfg.FetchTimeout, cfg.MaxDownloadBytes, fetch.Options{
		AllowedHosts:          cfg.AllowedHosts,
		BaseURL:               cfg.BaseURL,
		FollowRedirects:       cfg.FollowRedirects,
		AllowPrivateAddresses: cfg.AllowPrivateAddresses,
	})
	if err != nil {
		return fmt.Errorf("fetcher: %w", err)
	}

	if slices.Contains(cfg.AllowedHosts, "*") {
		logger.Warn("the allowlist is *: images are fetched from any host",
			slog.Bool("private_addresses_allowed", cfg.AllowPrivateAddresses))
	}

	ready := &httpapi.Readiness{}
	handler := httpapi.NewHandler(
		httpapi.Config{
			MaxPixels:       cfg.MaxPixels,
			XComponents:     cfg.XComponents,
			YComponents:     cfg.YComponents,
			MaxBodyBytes:    cfg.MaxDownloadBytes,
			BodyTimeout:     cfg.FetchTimeout,
			PathPrefix:      cfg.PathPrefix,
			MaxAge:          cfg.MaxAge,
			RequestIDHeader: cfg.RequestIDHeader,
		},
		fetcher,
		decodeQueue,
		ready,
		logger,
	)

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	server := newServer(handler, logger)
	logger.Info("listening", slog.String("addr", ln.Addr().String()))

	errc := make(chan error, 1)
	go func() { errc <- server.Serve(ln) }()

	ready.Set(true)

	var serveErr error

	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}

	ready.Set(false)
	logger.Info("shutting down", slog.Duration("timeout", cfg.ShutdownTimeout))

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	if err := errors.Join(serveErr, server.Shutdown(shutdownCtx)); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	logger.Info("stopped")

	return nil
}

// softLimit returns the soft memory limit to set: limit less
// softMemoryLimitHeadroomPercent percent. It returns 0, leaving the runtime's
// soft limit alone, when limit came from GOMEMLIMIT or is 0.
func softLimit(limit int64, source string) int64 {
	if source == config.FromGOMEMLIMIT || limit <= 0 {
		return 0
	}

	// Split by 100 first so limit times the percent cannot overflow.
	headroom := limit/100*softMemoryLimitHeadroomPercent + limit%100*softMemoryLimitHeadroomPercent/100

	return limit - headroom
}

func newServer(h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}
