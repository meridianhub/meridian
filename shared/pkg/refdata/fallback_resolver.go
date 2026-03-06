package refdata

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// SnapshotStorage provides durable persistence for last-known-good instrument data.
type SnapshotStorage interface {
	// Load reads the last-known-good snapshot. Returns nil slice if no snapshot exists.
	Load() ([]InstrumentProperties, error)

	// Save persists the current set of instruments as a snapshot.
	Save(instruments []InstrumentProperties) error
}

// FileSnapshotStorage implements SnapshotStorage using a JSON file with atomic writes.
type FileSnapshotStorage struct {
	path string
}

// NewFileSnapshotStorage creates a FileSnapshotStorage at the given path.
func NewFileSnapshotStorage(path string) *FileSnapshotStorage {
	return &FileSnapshotStorage{path: path}
}

// Load reads the snapshot from disk. Returns nil if the file doesn't exist.
func (f *FileSnapshotStorage) Load() ([]InstrumentProperties, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot: %w", err)
	}

	var instruments []InstrumentProperties
	if err := json.Unmarshal(data, &instruments); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return instruments, nil
}

// Save persists instruments using atomic write (temp file + fsync + rename).
func (f *FileSnapshotStorage) Save(instruments []InstrumentProperties) error {
	data, err := json.Marshal(instruments)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	dir := filepath.Dir(f.path)
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, f.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename snapshot: %w", err)
	}
	return nil
}

// FallbackResolverConfig holds configuration for the FallbackResolver.
type FallbackResolverConfig struct {
	// SnapshotInterval is how often to save snapshots. Default: 1 minute.
	SnapshotInterval time.Duration

	// Logger is the structured logger. Default: slog.Default().
	Logger *slog.Logger
}

const defaultSnapshotInterval = time.Minute

// ErrAlreadyStarted is returned when Start is called on a running FallbackResolver.
var ErrAlreadyStarted = fmt.Errorf("fallback resolver already started")

// FallbackResolver wraps a CachedResolver with snapshot-based fallback.
// When the upstream source is unavailable at startup, it loads from the
// last-known-good snapshot.
type FallbackResolver struct {
	primary  *CachedResolver
	storage  SnapshotStorage
	logger   *slog.Logger
	interval time.Duration

	fallbackActive atomic.Bool
	fallbackData   sync.Map // map[string]InstrumentProperties

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// Verify FallbackResolver implements InstrumentResolver.
var _ InstrumentResolver = (*FallbackResolver)(nil)

// NewFallbackResolver creates a FallbackResolver wrapping the given CachedResolver.
func NewFallbackResolver(primary *CachedResolver, storage SnapshotStorage, cfg FallbackResolverConfig) *FallbackResolver {
	if primary == nil {
		panic("refdata: primary resolver must not be nil")
	}
	if storage == nil {
		panic("refdata: snapshot storage must not be nil")
	}

	interval := cfg.SnapshotInterval
	if interval == 0 {
		interval = defaultSnapshotInterval
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &FallbackResolver{
		primary:  primary,
		storage:  storage,
		logger:   logger,
		interval: interval,
	}
}

// Start initializes the resolver: tries upstream preload, falls back to snapshot,
// and starts a background goroutine for periodic snapshots.
// Returns ErrAlreadyStarted if called while already running.
func (r *FallbackResolver) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return ErrAlreadyStarted
	}

	// Try upstream preload first
	if err := r.primary.Preload(ctx); err != nil {
		r.logger.Warn("upstream preload failed, loading snapshot", "error", err)

		instruments, loadErr := r.storage.Load()
		if loadErr != nil {
			return fmt.Errorf("load snapshot after upstream failure: %w", loadErr)
		}
		if instruments == nil {
			return fmt.Errorf("upstream unavailable and no snapshot exists: %w", err)
		}

		// Populate fallback data
		for _, props := range instruments {
			r.fallbackData.Store(props.Code, props)
		}
		r.fallbackActive.Store(true)
		r.logger.Warn("operating in fallback mode from snapshot", "instruments", len(instruments))
	} else {
		// Upstream succeeded - save initial snapshot
		r.saveSnapshot(ctx)
	}

	// The snapshot loop must outlive the Start() call's context and is controlled
	// by Stop() via the cancel function.
	bgCtx, cancel := context.WithCancel(context.Background()) //nolint:contextcheck // intentional detached context
	r.done = make(chan struct{})
	r.cancel = cancel
	r.started = true
	r.startSnapshotLoop(bgCtx) //nolint:contextcheck // detached context is intentional

	return nil
}

// Stop shuts down the background snapshot goroutine. Safe to call multiple times.
func (r *FallbackResolver) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	cancel := r.cancel
	done := r.done
	r.cancel = nil
	r.started = false
	r.mu.Unlock()

	cancel()
	<-done
}

// Resolve returns instrument properties. Uses primary (CachedResolver) when available,
// falls back to snapshot data. Fail-closed: returns ErrUnknownInstrument for unknown codes
// even in fallback mode.
func (r *FallbackResolver) Resolve(ctx context.Context, code string) (InstrumentProperties, error) {
	if !r.fallbackActive.Load() {
		return r.primary.Resolve(ctx, code)
	}

	// Fallback mode: use snapshot data
	val, ok := r.fallbackData.Load(code)
	if !ok {
		return InstrumentProperties{}, ErrUnknownInstrument
	}
	props, _ := val.(InstrumentProperties)
	return props, nil
}

// IsFallbackActive returns true when the resolver is operating from snapshot data
// instead of live upstream data.
func (r *FallbackResolver) IsFallbackActive() bool {
	return r.fallbackActive.Load()
}

func (r *FallbackResolver) startSnapshotLoop(ctx context.Context) {
	done := r.done // capture at launch-time to avoid reading r.done at close-time
	go r.snapshotLoop(ctx, done)
}

func (r *FallbackResolver) snapshotLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.saveSnapshot(ctx)
			// If in fallback mode, try to recover
			if r.fallbackActive.Load() {
				r.tryRecover(ctx)
			}
		}
	}
}

func (r *FallbackResolver) saveSnapshot(ctx context.Context) {
	instruments, err := r.primary.source.FetchAllActive(ctx)
	if err != nil {
		r.logger.Warn("failed to fetch instruments for snapshot", "error", err)
		return
	}
	if err := r.storage.Save(instruments); err != nil {
		r.logger.Warn("failed to save snapshot", "error", err)
	}
}

func (r *FallbackResolver) tryRecover(ctx context.Context) {
	if err := r.primary.Preload(ctx); err != nil {
		return // Still unavailable
	}
	// Recovery succeeded
	r.fallbackActive.Store(false)
	r.fallbackData.Range(func(key, _ any) bool {
		r.fallbackData.Delete(key)
		return true
	})
	r.logger.Info("recovered from fallback mode")
}
