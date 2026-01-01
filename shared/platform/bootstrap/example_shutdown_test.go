package bootstrap_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"google.golang.org/grpc"
)

// Example_signalHandler demonstrates proper signal handler usage with defer cleanup.
// The cleanup function MUST be deferred to prevent signal handler leaks.
func Example_signalHandler() {
	// Create signal handler - returns channel and cleanup function
	sigChan, cleanup := bootstrap.SignalHandler()
	defer cleanup() // CRITICAL: prevents signal handler resource leak

	// Use in a select with server error channel
	errChan := bootstrap.ServerErrorChannel(1)

	select {
	case sig := <-sigChan:
		fmt.Printf("Received signal: %v\n", sig)
	case err := <-errChan:
		fmt.Printf("Server error: %v\n", err)
	}
}

// Example_gracefulShutdown demonstrates shutting down multiple servers gracefully.
// This pattern works with any server implementing the ShutdownHandler interface
// (e.g., http.Server).
func Example_gracefulShutdown() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create HTTP servers
	httpServer := &http.Server{Addr: ":8080"}
	metricsServer := &http.Server{Addr: ":9090"}

	// Shutdown with timeout - shuts down each server in order
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := bootstrap.GracefulShutdown(ctx, logger, httpServer, metricsServer); err != nil {
		logger.Error("shutdown error", "error", err)
	}
}

// Example_completeServerLifecycle demonstrates a complete main() integration
// using SignalHandler, ServerErrorChannel, WaitForShutdownSignal, and GracefulShutdown.
// This is the recommended pattern for services with multiple servers.
func Example_completeServerLifecycle() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// 1. Set up signal handler FIRST (before starting any servers)
	sigChan, cleanup := bootstrap.SignalHandler()
	defer cleanup()

	// 2. Create servers
	httpServer := &http.Server{
		Addr:              ":8080",
		ReadHeaderTimeout: 10 * time.Second,
	}
	metricsServer := &http.Server{
		Addr:              ":9090",
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 3. Create error channel sized for ALL servers
	errChan := bootstrap.ServerErrorChannel(2) // 2 servers = buffer size 2

	// 4. Start servers in goroutines
	go func() {
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()
	go func() {
		if err := metricsServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	logger.Info("servers started", "http", ":8080", "metrics", ":9090")

	// 5. Wait for shutdown signal or server error
	if err := bootstrap.WaitForShutdownSignal(sigChan, errChan, logger); err != nil {
		logger.Error("server error triggered shutdown", "error", err)
	}

	// 6. Graceful shutdown of all servers
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := bootstrap.GracefulShutdown(ctx, logger, httpServer, metricsServer); err != nil {
		logger.Error("graceful shutdown error", "error", err)
	}

	logger.Info("shutdown complete")
}

// Example_shutdownOrchestrator demonstrates gRPC-specific shutdown using
// ShutdownOrchestrator with cleanup functions executed in LIFO order.
func Example_shutdownOrchestrator() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Create orchestrator
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger).
		WithTimeout(30 * time.Second)

	// Add cleanup functions - executed in REVERSE order (LIFO)
	// First added = last executed (like defer statements)
	orchestrator.AddCleanup(func() error {
		logger.Info("cleanup: closing database")
		return nil
	})
	orchestrator.AddCleanup(func() error {
		logger.Info("cleanup: stopping worker")
		return nil
	})

	// Start server
	lis, _ := (&net.ListenConfig{}).Listen(context.Background(), "tcp", ":50051")
	errChan := bootstrap.ServerErrorChannel(1)
	go func() {
		errChan <- grpcServer.Serve(lis)
	}()

	// Wait blocks until signal/error, then runs cleanup and graceful stop
	if err := orchestrator.Wait(errChan); err != nil {
		logger.Error("server error", "error", err)
	}
}
