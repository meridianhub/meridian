// Package await provides polling utilities for synchronizing test assertions.
//
// It eliminates time.Sleep by repeatedly checking a condition until it becomes
// true or a configurable timeout is reached. The fluent API expresses intent clearly
// and produces descriptive timeout errors.
//
// Default timeout is 10 seconds with a 100 ms poll interval.
//
// # Usage
//
//	// Simple condition wait
//	err := await.Until(func() bool {
//	    return repo.FindByID(ctx, id) != nil
//	})
//
//	// Custom timeout and interval
//	err := await.New().
//	    AtMost(5 * time.Second).
//	    PollInterval(50 * time.Millisecond).
//	    Until(func() bool { return order.Status == "COMPLETED" })
//
//	// Wait for an operation to stop returning errors
//	err := await.UntilNoError(func() error {
//	    return client.HealthCheck()
//	})
//
// Note: For advanced matchers or async assertions, consider [gomega.Eventually].
package await
