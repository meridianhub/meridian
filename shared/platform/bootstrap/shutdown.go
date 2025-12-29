package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
)

// DefaultShutdownTimeout is the default timeout for graceful shutdown.
const DefaultShutdownTimeout = 30 * time.Second

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
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

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
