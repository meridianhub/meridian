package dispatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/await"
)

// stubFetcher is a test double for InstructionFetcher.
type stubFetcher struct {
	instructions []*mockInstruction
	err          error
	callCount    atomic.Int32
}

func (f *stubFetcher) FetchDispatchable(_ context.Context, _ int) ([]*mockInstruction, error) {
	f.callCount.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.instructions, nil
}

func TestWorkerConfig_ApplyDefaults(t *testing.T) {
	cfg := WorkerConfig{}
	cfg.applyDefaults()
	assert.Equal(t, DefaultBatchSize, cfg.BatchSize)
	assert.Equal(t, DefaultPollInterval, cfg.PollInterval)
}

func TestWorkerConfig_ApplyDefaultsPreservesValues(t *testing.T) {
	cfg := WorkerConfig{
		BatchSize:    10,
		PollInterval: 500 * time.Millisecond,
	}
	cfg.applyDefaults()
	assert.Equal(t, 10, cfg.BatchSize)
	assert.Equal(t, 500*time.Millisecond, cfg.PollInterval)
}

func TestWorker_StartAndStop(t *testing.T) {
	fetcher := &stubFetcher{}
	var processed atomic.Int32
	processor := func(_ context.Context, instructions []*mockInstruction) {
		processed.Add(int32(len(instructions)))
	}

	w := NewWorker[*mockInstruction](fetcher, processor, WorkerConfig{PollInterval: 10 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	// Wait for at least one poll cycle
	err := await.Until(func() bool {
		return fetcher.callCount.Load() > 0
	})
	require.NoError(t, err)

	w.Stop()

	// Should have polled but processed zero (no instructions returned)
	assert.Equal(t, int32(0), processed.Load())
}

func TestWorker_StartIdempotent(_ *testing.T) {
	fetcher := &stubFetcher{}
	processor := func(_ context.Context, _ []*mockInstruction) {}

	w := NewWorker[*mockInstruction](fetcher, processor, WorkerConfig{PollInterval: 10 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)
	w.Start(ctx) // second call is a no-op
	w.Stop()
}

func TestWorker_ProcessesBatch(t *testing.T) {
	instructions := []*mockInstruction{
		{id: "1", tenantID: "t", instructionType: "pay", connectionID: "c", status: InstructionStatusPending},
		{id: "2", tenantID: "t", instructionType: "pay", connectionID: "c", status: InstructionStatusPending},
	}
	fetcher := &stubFetcher{instructions: instructions}

	var processed atomic.Int32
	processor := func(_ context.Context, batch []*mockInstruction) {
		processed.Add(int32(len(batch)))
	}

	w := NewWorker[*mockInstruction](fetcher, processor, WorkerConfig{PollInterval: 10 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	err := await.Until(func() bool {
		return processed.Load() >= 2
	})
	require.NoError(t, err)

	w.Stop()
	assert.GreaterOrEqual(t, processed.Load(), int32(2))
}

func TestWorker_HandlesErrorFromFetcher(t *testing.T) {
	fetcher := &stubFetcher{err: errors.New("db connection lost")}

	var processed atomic.Int32
	processor := func(_ context.Context, _ []*mockInstruction) {
		processed.Add(1)
	}

	w := NewWorker[*mockInstruction](fetcher, processor, WorkerConfig{PollInterval: 10 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	// Let a few poll cycles run
	err := await.Until(func() bool {
		return fetcher.callCount.Load() >= 3
	})
	require.NoError(t, err)

	w.Stop()

	// Processor should never have been called
	assert.Equal(t, int32(0), processed.Load())
}

func TestWorker_StopsOnContextCancellation(t *testing.T) {
	fetcher := &stubFetcher{}
	processor := func(_ context.Context, _ []*mockInstruction) {}

	w := NewWorker[*mockInstruction](fetcher, processor, WorkerConfig{PollInterval: 10 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	// Wait for at least one poll
	err := await.Until(func() bool {
		return fetcher.callCount.Load() > 0
	})
	require.NoError(t, err)

	// Cancel context — worker should stop
	cancel()

	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}
