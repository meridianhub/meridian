package bootstrap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeClient is a test double for LazyClient resolution.
type fakeClient struct {
	Name string
}

func TestLazyClient_GetBeforeResolution_ReturnsUnavailable(t *testing.T) {
	// resolve blocks forever so Get is always called before resolution
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lc := NewLazyClient[*fakeClient](ctx, "test-client", func(ctx context.Context) (*fakeClient, func(), error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})

	_, err := lc.Get()
	if err == nil {
		t.Fatal("Get() before resolution should return error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("Get() status code = %v, want %v", st.Code(), codes.Unavailable)
	}
}

func TestLazyClient_GetAfterResolution_ReturnsClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := &fakeClient{Name: "resolved"}
	lc := NewLazyClient[*fakeClient](ctx, "test-client", func(_ context.Context) (*fakeClient, func(), error) {
		return want, func() {}, nil
	})

	// Wait for background resolution
	waitForReady(t, lc, 5*time.Second)

	got, err := lc.Get()
	if err != nil {
		t.Fatalf("Get() after resolution: unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Get() = %v, want %v", got, want)
	}
}

func TestLazyClient_IsReady_FalseBeforeTrueAfter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolved := make(chan struct{})
	lc := NewLazyClient[*fakeClient](ctx, "test-client", func(_ context.Context) (*fakeClient, func(), error) {
		<-resolved
		return &fakeClient{Name: "ready"}, func() {}, nil
	})

	if lc.IsReady() {
		t.Error("IsReady() before resolution = true, want false")
	}

	close(resolved)
	waitForReady(t, lc, 5*time.Second)

	if !lc.IsReady() {
		t.Error("IsReady() after resolution = false, want true")
	}
}

func TestLazyClient_RetriesOnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int32
	lc := NewLazyClient[*fakeClient](ctx, "test-client", func(_ context.Context) (*fakeClient, func(), error) {
		n := attempts.Add(1)
		if n < 3 {
			return nil, nil, errors.New("connection refused")
		}
		return &fakeClient{Name: "eventually"}, func() {}, nil
	}, WithLazyInitialWait(1*time.Millisecond), WithLazyMaxWait(5*time.Millisecond))

	waitForReady(t, lc, 5*time.Second)

	got, err := lc.Get()
	if err != nil {
		t.Fatalf("Get() after retry: unexpected error: %v", err)
	}
	if got.Name != "eventually" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "eventually")
	}
	if n := attempts.Load(); n < 3 {
		t.Errorf("resolve called %d times, want >= 3", n)
	}
}

func TestLazyClient_ContextCancellation_StopsBackground(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var attempts atomic.Int32
	blocked := make(chan struct{})

	_ = NewLazyClient[*fakeClient](ctx, "test-client", func(_ context.Context) (*fakeClient, func(), error) {
		n := attempts.Add(1)
		if n == 1 {
			close(blocked)
		}
		return nil, nil, errors.New("connection refused")
	}, WithLazyInitialWait(1*time.Millisecond), WithLazyMaxWait(5*time.Millisecond))

	// Wait for at least one attempt
	<-blocked

	// Cancel context to stop background goroutine
	cancel()

	//nolint:forbidigo // gives goroutine time to observe context cancellation before sampling attempt count
	time.Sleep(50 * time.Millisecond)

	countAfterCancel := attempts.Load()
	//nolint:forbidigo // ensures no new attempts are made after context cancellation (absence of activity)
	time.Sleep(50 * time.Millisecond)

	if got := attempts.Load(); got != countAfterCancel {
		t.Errorf("attempts increased from %d to %d after context cancellation", countAfterCancel, got)
	}
}

func TestLazyClient_CleanupAppended(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var cleaned atomic.Bool
	cleanupFn := func() { cleaned.Store(true) }

	var mu sync.Mutex
	var cleanups []func()

	lc := NewLazyClient[*fakeClient](ctx, "test-client", func(_ context.Context) (*fakeClient, func(), error) {
		return &fakeClient{Name: "ok"}, cleanupFn, nil
	}, WithLazyOnCleanup(func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		cleanups = append(cleanups, fn)
	}))

	waitForReady(t, lc, 5*time.Second)

	mu.Lock()
	n := len(cleanups)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("cleanup slice len = %d, want 1", n)
	}

	mu.Lock()
	fn := cleanups[0]
	mu.Unlock()
	fn()
	if !cleaned.Load() {
		t.Error("cleanup function was not called")
	}
}

func TestLazyClient_ConcurrentGet_NoRace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolved := make(chan struct{})
	want := &fakeClient{Name: "concurrent"}
	lc := NewLazyClient[*fakeClient](ctx, "test-client", func(_ context.Context) (*fakeClient, func(), error) {
		<-resolved
		return want, func() {}, nil
	})

	var wg sync.WaitGroup
	const goroutines = 20

	// Start concurrent Get() calls before resolution
	errCh := make(chan error, goroutines)
	clientCh := make(chan *fakeClient, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Some calls before resolution, some after
			c, err := lc.Get()
			if err != nil {
				errCh <- err
			} else {
				clientCh <- c
			}
		}()
	}

	// Unblock resolution partway through
	close(resolved)
	wg.Wait()
	close(errCh)
	close(clientCh)

	// All results should be either Unavailable error or the correct client
	for err := range errCh {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unavailable {
			t.Errorf("concurrent Get() error = %v, want codes.Unavailable", err)
		}
	}
	for c := range clientCh {
		if c != want {
			t.Errorf("concurrent Get() = %v, want %v", c, want)
		}
	}
}

// waitForReady polls IsReady() until true or timeout.
func waitForReady[T any](t *testing.T, lc *LazyClient[T], timeout time.Duration) {
	t.Helper()
	if err := await.AtMost(timeout).PollInterval(5 * time.Millisecond).Until(lc.IsReady); err != nil {
		t.Fatal("LazyClient did not become ready within timeout")
	}
}
