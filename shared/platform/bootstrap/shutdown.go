package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"google.golang.org/grpc"
)

// DefaultShutdownTimeout is the default timeout for graceful shutdown.
//
// Deprecated: Use defaults.DefaultGracefulShutdown instead.
var DefaultShutdownTimeout = defaults.DefaultGracefulShutdown

// ShutdownOrchestrator coordinates graceful shutdown of a gRPC service.
// It handles OS signals (SIGINT, SIGTERM), executes cleanup functions in reverse order,
// and performs graceful gRPC server shutdown with timeout fallback.
type ShutdownOrchestrator struct {
	grpcServer      *grpc.Server
	logger          *slog.Logger
	shutdownTimeout time.Duration
	cleanupFuncs    []func() error
}

// NewShutdownOrchestrator creates a new ShutdownOrchestrator for the given gRPC server.
// The default shutdown timeout is 30 seconds.
func NewShutdownOrchestrator(grpcServer *grpc.Server, logger *slog.Logger) *ShutdownOrchestrator {
	return &ShutdownOrchestrator{
		grpcServer:      grpcServer,
		logger:          logger,
		shutdownTimeout: DefaultShutdownTimeout,
		cleanupFuncs:    make([]func() error, 0),
	}
}

// WithTimeout sets a custom shutdown timeout.
// Returns the orchestrator for method chaining.
func (s *ShutdownOrchestrator) WithTimeout(timeout time.Duration) *ShutdownOrchestrator {
	s.shutdownTimeout = timeout
	return s
}

// AddCleanup registers a cleanup function to be executed during shutdown.
// Cleanup functions are executed in reverse order (LIFO) of registration.
// If a cleanup function returns an error, it is logged but shutdown continues.
func (s *ShutdownOrchestrator) AddCleanup(fn func() error) {
	s.cleanupFuncs = append(s.cleanupFuncs, fn)
}

// SignalHandler creates a signal channel configured to receive SIGINT and SIGTERM.
// Returns the channel and a cleanup function. The caller MUST defer the cleanup
// function to prevent resource leaks.
//
// Example:
//
//	sigChan, cleanup := bootstrap.SignalHandler()
//	defer cleanup()
//	<-sigChan // Wait for shutdown signal
func SignalHandler() (chan os.Signal, func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	cleanup := func() { signal.Stop(sigChan) }
	return sigChan, cleanup
}

// Wait blocks until a shutdown signal is received (SIGINT, SIGTERM) or a server error occurs.
// When triggered, it:
//  1. Logs the trigger reason (signal or error)
//  2. Executes cleanup functions in reverse order (LIFO)
//  3. Initiates graceful gRPC shutdown with GracefulStop
//  4. Falls back to immediate Stop if timeout is exceeded
//
// Returns the original server error if shutdown was triggered by one, nil otherwise.
func (s *ShutdownOrchestrator) Wait(serverErrors <-chan error) error {
	// Set up signal handling
	sigChan, signalCleanup := SignalHandler()
	defer signalCleanup()

	// Wait for signal or error
	var serverErr error
	select {
	case sig := <-sigChan:
		s.logger.Info("received signal, initiating shutdown", "signal", sig)
	case serverErr = <-serverErrors:
		s.logger.Error("server error, initiating shutdown", "error", serverErr)
	}

	// Execute cleanup functions in reverse order (LIFO)
	s.logger.Info("executing cleanup functions", "count", len(s.cleanupFuncs))
	for i := len(s.cleanupFuncs) - 1; i >= 0; i-- {
		if err := s.cleanupFuncs[i](); err != nil {
			s.logger.Error("cleanup function failed", "index", i, "error", err)
		}
	}

	// Graceful gRPC shutdown with timeout
	s.logger.Info("shutting down gRPC server", "timeout", s.shutdownTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	stopped := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		s.logger.Info("gRPC server stopped gracefully")
	case <-ctx.Done():
		s.logger.Warn("graceful shutdown timeout exceeded, forcing stop")
		s.grpcServer.Stop()
	}

	return serverErr
}

// ShutdownHandler is an interface for services that support graceful shutdown.
// Any server or service that can be gracefully stopped should implement this interface.
// Standard library servers like http.Server already implement this interface.
type ShutdownHandler interface {
	Shutdown(context.Context) error
}

// GracefulShutdown handles graceful shutdown of multiple servers with timeout.
// It iterates through all servers, calling Shutdown on each with the provided context.
// Errors are logged but shutdown continues for remaining servers to ensure all
// resources are cleaned up. Returns the first error encountered or nil if all
// servers shut down successfully.
//
// The function uses defaults.DefaultGracefulShutdown (30s) as the timeout if the
// provided context does not have a deadline.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	err := bootstrap.GracefulShutdown(ctx, logger, httpServer, grpcServer)
func GracefulShutdown(ctx context.Context, logger *slog.Logger, servers ...ShutdownHandler) error {
	// Ensure we have a deadline
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaults.DefaultGracefulShutdown)
		defer cancel()
	}

	var firstErr error
	for i, server := range servers {
		if server == nil {
			continue
		}
		logger.Info("shutting down server", "index", i, "total", len(servers))
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("server shutdown failed", "index", i, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ServerErrorChannel creates a buffered channel for collecting server errors.
// The buffer size equals serverCount to prevent deadlocks when multiple servers
// report errors simultaneously. This is essential for proper error handling in
// concurrent server startup scenarios.
//
// Example:
//
//	errChan := bootstrap.ServerErrorChannel(2)
//	go func() { errChan <- httpServer.ListenAndServe() }()
//	go func() { errChan <- grpcServer.Serve(lis) }()
func ServerErrorChannel(serverCount int) chan error {
	return make(chan error, serverCount)
}

// WaitForShutdownSignal blocks until either a shutdown signal (SIGINT/SIGTERM)
// is received or a server error occurs on the error channel. It returns nil
// for a clean shutdown signal, or the server error if shutdown was triggered
// by a server failure.
//
// This is a standalone utility for simple cases where the full ShutdownOrchestrator
// is not needed. For more complex scenarios with cleanup functions and gRPC-specific
// shutdown, use ShutdownOrchestrator instead.
//
// Example:
//
//	sigChan, cleanup := bootstrap.SignalHandler()
//	defer cleanup()
//	errChan := bootstrap.ServerErrorChannel(1)
//	go func() { errChan <- server.ListenAndServe() }()
//	if err := bootstrap.WaitForShutdownSignal(sigChan, errChan, logger); err != nil {
//	    logger.Error("server error", "error", err)
//	}
func WaitForShutdownSignal(sigChan chan os.Signal, errChan chan error, logger *slog.Logger) error {
	select {
	case sig := <-sigChan:
		logger.Info("received shutdown signal", "signal", sig)
		return nil
	case err := <-errChan:
		logger.Error("server error, initiating shutdown", "error", err)
		return err
	}
}
