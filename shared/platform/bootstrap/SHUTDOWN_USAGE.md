# Shutdown Utilities Usage Guide

This document provides migration guidance for adopting the shared shutdown utilities in `shared/platform/bootstrap`.

## Overview

The shutdown utilities provide:

- **SignalHandler**: Creates SIGINT/SIGTERM handler with automatic cleanup
- **ServerErrorChannel**: Properly-sized buffered channel for server errors
- **WaitForShutdownSignal**: Blocks until signal or error
- **GracefulShutdown**: Shuts down multiple servers with timeout
- **ShutdownOrchestrator**: Full gRPC lifecycle with cleanup functions

## Migration Guide

### Signal Handler Migration

**Before** (scattered signal handling, leak-prone):

```go
// Manual signal setup - easy to forget cleanup
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
// ... later, maybe:
// signal.Stop(sigChan)  // Often forgotten, causing resource leak
```

**After** (cleanup guaranteed):

```go
sigChan, cleanup := bootstrap.SignalHandler()
defer cleanup()  // Always called, prevents resource leak
```

### Error Channel Migration

**Before** (unbuffered or incorrectly sized):

```go
// WRONG: Unbuffered channel deadlocks if both servers fail simultaneously
serverErrors := make(chan error)

// WRONG: Buffer size 1 with 2 servers still risks deadlock
serverErrors := make(chan error, 1)
go func() { serverErrors <- httpServer.ListenAndServe() }()
go func() { serverErrors <- grpcServer.Serve(lis) }()
```

**After** (correctly sized):

```go
// Buffer size matches server count - no deadlock risk
serverErrors := bootstrap.ServerErrorChannel(2)
go func() { serverErrors <- httpServer.ListenAndServe() }()
go func() { serverErrors <- grpcServer.Serve(lis) }()
```

### Wait Pattern Migration

**Before** (manual select):

```go
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

select {
case sig := <-sigChan:
    logger.Info("received signal", "signal", sig)
case err := <-serverErrors:
    return fmt.Errorf("server error: %w", err)
}
signal.Stop(sigChan)  // Must remember to call
```

**After** (encapsulated):

```go
sigChan, cleanup := bootstrap.SignalHandler()
defer cleanup()

if err := bootstrap.WaitForShutdownSignal(sigChan, serverErrors, logger); err != nil {
    return fmt.Errorf("server error: %w", err)
}
```

### Multi-Server Shutdown Migration

**Before** (manual iteration):

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := httpServer.Shutdown(ctx); err != nil {
    logger.Error("http shutdown error", "error", err)
}
if err := metricsServer.Shutdown(ctx); err != nil {
    logger.Error("metrics shutdown error", "error", err)
}
```

**After** (single call):

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := bootstrap.GracefulShutdown(ctx, logger, httpServer, metricsServer); err != nil {
    logger.Error("shutdown error", "error", err)
}
```

### gRPC with Cleanup Functions

**Before** (manual cleanup ordering):

```go
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
<-sigChan
signal.Stop(sigChan)

// Manual cleanup - easy to get order wrong
worker.Stop()
db.Close()
grpcServer.GracefulStop()
```

**After** (LIFO cleanup, timeout fallback):

```go
orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

// Cleanup functions run in REVERSE order (like defer)
orchestrator.AddCleanup(func() error {
    bootstrap.CloseDatabase(db, logger)
    return nil
})
orchestrator.AddCleanup(func() error {
    worker.Stop()
    return nil
})

// Handles signal, runs cleanup, graceful stop with timeout fallback
return orchestrator.Wait(serverErrors)
```

## Benefits

| Old Pattern | Problem | New Pattern | Solution |
|-------------|---------|-------------|----------|
| Manual signal.Notify | signal.Stop often forgotten | SignalHandler() | Returns cleanup func to defer |
| Unbuffered error chan | Deadlock on multiple failures | ServerErrorChannel(n) | Buffer sized to server count |
| Manual shutdown loop | Inconsistent error handling | GracefulShutdown() | Logs errors, continues shutdown |
| No timeout fallback | Hanging on stuck servers | ShutdownOrchestrator | Force stop after timeout |

## Migration Candidates

Services currently using manual patterns that should migrate:

- `services/tenant/cmd/main.go` - Uses ShutdownOrchestrator (already migrated)
- `services/payment-order/cmd/main.go` - Manual signal handling
- `services/financial-accounting/cmd/main.go` - Manual signal handling
- `services/current-account/cmd/main.go` - Manual signal handling
- `services/position-keeping/cmd/main.go` - Manual signal handling
- `services/party/cmd/main.go` - Manual signal handling

## Complete Example

```go
func run(logger *slog.Logger) error {
    // 1. Signal handler first
    sigChan, cleanup := bootstrap.SignalHandler()
    defer cleanup()

    // 2. Create servers
    grpcServer := grpc.NewServer()
    httpServer := &http.Server{Addr: ":8080"}

    // 3. Properly sized error channel
    errChan := bootstrap.ServerErrorChannel(2)

    // 4. Start servers
    go func() { errChan <- grpcServer.Serve(lis) }()
    go func() {
        if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
            errChan <- err
        }
    }()

    // 5. Wait for shutdown trigger
    serverErr := bootstrap.WaitForShutdownSignal(sigChan, errChan, logger)

    // 6. Graceful shutdown
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    grpcServer.GracefulStop()
    _ = bootstrap.GracefulShutdown(ctx, logger, httpServer)

    return serverErr
}
```
