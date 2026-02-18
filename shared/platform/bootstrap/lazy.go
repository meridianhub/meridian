package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ResolveFunc attempts to create a client of type T. On success it returns the
// client value, a cleanup function (called during shutdown), and nil error.
// On failure it returns a non-nil error, and the LazyClient background
// goroutine will retry with exponential backoff.
type ResolveFunc[T any] func(ctx context.Context) (T, func(), error)

// LazyClient wraps an optional dependency that resolves asynchronously in the
// background. This allows the gRPC server to start immediately and health
// probes to pass while optional clients connect with retry.
type LazyClient[T any] struct {
	name    string
	client  atomic.Pointer[T]
	resolve ResolveFunc[T]
}

// lazyConfig holds configuration for NewLazyClient.
type lazyConfig struct {
	initialWait time.Duration
	maxWait     time.Duration
	multiplier  float64
	logger      *slog.Logger
	onCleanup   func(func())
}

// LazyOption configures LazyClient behavior.
type LazyOption func(*lazyConfig)

// WithLazyInitialWait sets the initial backoff wait between retries (default: 1s).
func WithLazyInitialWait(d time.Duration) LazyOption {
	return func(c *lazyConfig) { c.initialWait = d }
}

// WithLazyMaxWait sets the maximum backoff wait between retries (default: 30s).
func WithLazyMaxWait(d time.Duration) LazyOption {
	return func(c *lazyConfig) { c.maxWait = d }
}

// WithLazyLogger sets a structured logger for resolution events.
func WithLazyLogger(l *slog.Logger) LazyOption {
	return func(c *lazyConfig) { c.logger = l }
}

// WithLazyOnCleanup registers a callback that is invoked with the resolve
// function's cleanup func on successful resolution. The caller can use this
// to append to a cleanup slice with appropriate synchronization.
func WithLazyOnCleanup(fn func(func())) LazyOption {
	return func(c *lazyConfig) { c.onCleanup = fn }
}

// NewLazyClient creates a LazyClient that resolves its dependency in a background
// goroutine with exponential backoff retry. The background goroutine stops when
// ctx is cancelled or resolution succeeds.
func NewLazyClient[T any](ctx context.Context, name string, resolve ResolveFunc[T], opts ...LazyOption) *LazyClient[T] {
	cfg := lazyConfig{
		initialWait: 1 * time.Second,
		maxWait:     30 * time.Second,
		multiplier:  2.0,
	}
	for _, o := range opts {
		o(&cfg)
	}

	lc := &LazyClient[T]{
		name:    name,
		resolve: resolve,
	}

	go lc.backgroundResolve(ctx, cfg)

	return lc
}

// Get returns the resolved client or a gRPC Unavailable status error if the
// client has not been resolved yet.
func (lc *LazyClient[T]) Get() (T, error) {
	if p := lc.client.Load(); p != nil {
		return *p, nil
	}
	var zero T
	return zero, status.Errorf(codes.Unavailable, "%s is not yet available", lc.name)
}

// IsReady reports whether the client has been successfully resolved.
func (lc *LazyClient[T]) IsReady() bool {
	return lc.client.Load() != nil
}

// backgroundResolve runs in a goroutine and attempts to resolve the client with
// exponential backoff until success or context cancellation.
func (lc *LazyClient[T]) backgroundResolve(ctx context.Context, cfg lazyConfig) {
	wait := cfg.initialWait

	for {
		client, cleanup, err := lc.resolve(ctx)
		if err == nil {
			lc.client.Store(&client)
			if cfg.onCleanup != nil && cleanup != nil {
				cfg.onCleanup(cleanup)
			}
			if cfg.logger != nil {
				cfg.logger.Info(fmt.Sprintf("lazy client %q resolved", lc.name))
			}
			return
		}

		if cfg.logger != nil {
			cfg.logger.Warn(fmt.Sprintf("lazy client %q resolve failed, retrying", lc.name),
				"error", err,
				"next_wait", wait,
			)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		// Exponential backoff, capped at maxWait
		wait = time.Duration(float64(wait) * cfg.multiplier)
		if wait > cfg.maxWait {
			wait = cfg.maxWait
		}
	}
}
