// Package scheduler provides shared lifecycle management for background workers
// and scheduled tasks. It extracts common patterns (start/stop, graceful shutdown,
// WaitGroup tracking) found across event outbox workers, audit workers, and
// forecasting schedulers into a reusable abstraction.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Lifecycle errors.
var (
	// ErrAlreadyRunning is returned when Start is called on a lifecycle that is already running.
	ErrAlreadyRunning = errors.New("worker is already running")

	// ErrAlreadyStopped is returned when Start is called on a lifecycle that has been stopped.
	ErrAlreadyStopped = errors.New("worker has been stopped")
)

// noCopy may be added to structs which must not be copied after first use.
// See https://golang.org/issues/8005#issuecomment-190753527 for details.
// This is detected by go vet -copylocks.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// WorkerLifecycle provides start/stop lifecycle management, WaitGroup tracking
// for in-flight work, and graceful shutdown with timeout. It is designed to be
// embedded in concrete worker implementations.
//
// WorkerLifecycle must not be copied after first use because it contains
// sync primitives (sync.Mutex, sync.WaitGroup). Always use a pointer.
//
// The zero value is safe to use; fields are lazily initialized on first method
// call. However, using NewWorkerLifecycle is preferred to supply a logger.
type WorkerLifecycle struct {
	noCopy  noCopy
	mu      sync.Mutex
	wg      sync.WaitGroup
	running bool
	stopped bool
	done    chan struct{}
	cancel  context.CancelFunc
	logger  *slog.Logger
}

// initLocked lazily initializes fields that require non-zero values.
// Must be called while w.mu is held.
func (w *WorkerLifecycle) initLocked() {
	if w.done == nil {
		w.done = make(chan struct{})
	}
	if w.logger == nil {
		w.logger = slog.Default()
	}
}

// NewWorkerLifecycle creates a new WorkerLifecycle.
// If logger is nil, slog.Default() is used.
func NewWorkerLifecycle(logger *slog.Logger) *WorkerLifecycle {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkerLifecycle{
		done:   make(chan struct{}),
		logger: logger,
	}
}

// Start begins the worker lifecycle by executing workFunc. The provided workFunc
// receives a context that is cancelled when Stop is called.
//
// Returns ErrAlreadyRunning if the lifecycle is already started.
// Returns ErrAlreadyStopped if the lifecycle has been stopped (lifecycles are single-use).
func (w *WorkerLifecycle) Start(ctx context.Context, workFunc func(context.Context) error) error {
	w.mu.Lock()
	w.initLocked()
	if w.stopped {
		w.mu.Unlock()
		return ErrAlreadyStopped
	}
	if w.running {
		w.mu.Unlock()
		return ErrAlreadyRunning
	}
	w.running = true

	workCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()

	return workFunc(workCtx)
}

// Stop initiates graceful shutdown. It signals the worker to stop via context
// cancellation, then waits up to timeout for in-flight guarded work to complete.
// Safe to call multiple times and concurrently.
func (w *WorkerLifecycle) Stop(timeout time.Duration) {
	w.mu.Lock()
	w.initLocked()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.running = false
	if w.cancel != nil {
		w.cancel()
	}
	close(w.done)
	w.mu.Unlock()

	waitDone := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		w.logger.Info("worker shutdown completed")
	case <-time.After(timeout):
		w.logger.Warn("worker shutdown timeout exceeded", "timeout", timeout)
	}
}

// ExecuteGuarded executes fn while tracking it as in-flight work. If the
// lifecycle has been stopped, fn is not executed. The WaitGroup is incremented
// before fn runs and decremented after fn returns, ensuring Stop waits for
// all guarded work to complete.
func (w *WorkerLifecycle) ExecuteGuarded(fn func()) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.wg.Add(1)
	w.mu.Unlock()
	defer w.wg.Done()

	fn()
}

// Done returns a channel that is closed when Stop is called.
// This can be used in select statements to detect shutdown.
func (w *WorkerLifecycle) Done() <-chan struct{} {
	w.mu.Lock()
	w.initLocked()
	ch := w.done
	w.mu.Unlock()
	return ch
}

// IsRunning returns true if the lifecycle has been started and not stopped.
func (w *WorkerLifecycle) IsRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}
