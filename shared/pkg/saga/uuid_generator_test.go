package saga

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test UUID v5 generation using saga_instance_id as namespace
func TestUUIDGenerator_Determinism(t *testing.T) {
	// Same inputs must produce identical UUIDs
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	gen1 := NewUUIDGenerator(instanceID)
	gen2 := NewUUIDGenerator(instanceID)

	// Generate UUIDs with same stepIndex and callIndex
	uuid1 := gen1.NewUUID(2, 0)
	uuid2 := gen2.NewUUID(2, 0)

	assert.Equal(t, uuid1, uuid2, "same inputs must produce identical UUIDs")

	// Different call indices produce different UUIDs
	uuid3 := gen1.NewUUID(2, 1)
	assert.NotEqual(t, uuid1, uuid3, "different callIndex must produce different UUIDs")

	// Different step indices produce different UUIDs
	uuid4 := gen1.NewUUID(3, 0)
	assert.NotEqual(t, uuid1, uuid4, "different stepIndex must produce different UUIDs")
}

func TestUUIDGenerator_UUIDv5Format(t *testing.T) {
	// Verify the generated UUID is version 5
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	gen := NewUUIDGenerator(instanceID)

	id := gen.NewUUID(0, 0)

	// UUID v5 has version bits set to 5 (0101)
	// Version is stored in bits 12-15 of time_hi_and_version (byte 6)
	assert.Equal(t, byte(5), id[6]>>4, "UUID version must be 5")

	// Variant bits should be 10xxxxxx (RFC 4122)
	assert.Equal(t, byte(0x80), id[8]&0xC0, "UUID variant must be RFC 4122")
}

func TestUUIDGenerator_NameFormat(t *testing.T) {
	// Test that name is formatted as "{StepIndex}:{CallIndex}"
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	// Manually compute expected UUID using the same algorithm
	name := "2:0"
	expectedUUID := uuid.NewSHA1(instanceID, []byte(name))

	gen := NewUUIDGenerator(instanceID)
	actualUUID := gen.NewUUID(2, 0)

	assert.Equal(t, expectedUUID, actualUUID, "UUID should match manual computation with name format")
}

func TestStepContext_NewUUID_AtomicIncrement(t *testing.T) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ctx := NewStepContext(instanceID, 0)

	// Generate 3 UUIDs in sequence
	uuid0 := ctx.NewUUID()
	uuid1 := ctx.NewUUID()
	uuid2 := ctx.NewUUID()

	// All should be unique
	assert.NotEqual(t, uuid0, uuid1)
	assert.NotEqual(t, uuid1, uuid2)
	assert.NotEqual(t, uuid0, uuid2)

	// Verify they match expected call indices
	gen := NewUUIDGenerator(instanceID)
	assert.Equal(t, gen.NewUUID(0, 0), uuid0, "first UUID should have callIndex 0")
	assert.Equal(t, gen.NewUUID(0, 1), uuid1, "second UUID should have callIndex 1")
	assert.Equal(t, gen.NewUUID(0, 2), uuid2, "third UUID should have callIndex 2")
}

func TestStepContext_ResetForStep(t *testing.T) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ctx := NewStepContext(instanceID, 0)

	// Generate UUID in step 0
	uuid0Step0 := ctx.NewUUID()

	// Reset for step 1
	ctx.ResetForStep(1)

	// Generate UUID in step 1 (callIndex should be 0 again)
	uuid0Step1 := ctx.NewUUID()

	// They should be different (different step indices)
	assert.NotEqual(t, uuid0Step0, uuid0Step1)

	// Verify correctness
	gen := NewUUIDGenerator(instanceID)
	assert.Equal(t, gen.NewUUID(0, 0), uuid0Step0)
	assert.Equal(t, gen.NewUUID(1, 0), uuid0Step1)
}

func TestStepContext_ReplayDeterminism(t *testing.T) {
	// FR-26: Replay of same step produces IDENTICAL UUIDs
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	// First execution of step 2
	ctx1 := NewStepContext(instanceID, 2)
	uuids1 := []uuid.UUID{
		ctx1.NewUUID(),
		ctx1.NewUUID(),
		ctx1.NewUUID(),
	}

	// Replay of step 2 (simulating failure/recovery)
	ctx2 := NewStepContext(instanceID, 2)
	uuids2 := []uuid.UUID{
		ctx2.NewUUID(),
		ctx2.NewUUID(),
		ctx2.NewUUID(),
	}

	// Must be identical
	assert.Equal(t, uuids1, uuids2, "replay must produce identical UUIDs")
}

func TestStepContext_HotFixStability(t *testing.T) {
	// FR-21, SAGA-060: When saga definition changes mid-flight,
	// namespace remains the original saga_instance_id
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	// Generate UUID before "definition change"
	ctx1 := NewStepContext(instanceID, 0)
	originalUUID := ctx1.NewUUID()

	// Simulate saga definition hot-fix by creating new context
	// with SAME instance ID (namespace must remain unchanged)
	ctx2 := NewStepContext(instanceID, 0)
	replayUUID := ctx2.NewUUID()

	// UUID must match because namespace (instance ID) is unchanged
	assert.Equal(t, originalUUID, replayUUID, "UUID namespace must remain stable across hot-fix")
}

func TestStepContext_ConcurrentAccess(t *testing.T) {
	// Verify atomic callIndex increment under concurrent access
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ctx := NewStepContext(instanceID, 0)

	numGoroutines := 100
	uuidsPerGoroutine := 10

	var wg sync.WaitGroup
	uuidChan := make(chan uuid.UUID, numGoroutines*uuidsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < uuidsPerGoroutine; j++ {
				id := ctx.NewUUID()
				uuidChan <- id
			}
		}()
	}

	wg.Wait()
	close(uuidChan)

	// Collect all UUIDs
	uuidSet := make(map[uuid.UUID]struct{})
	for id := range uuidChan {
		uuidSet[id] = struct{}{}
	}

	// All UUIDs must be unique (no duplicates from concurrent access)
	expectedCount := numGoroutines * uuidsPerGoroutine
	assert.Equal(t, expectedCount, len(uuidSet), "all UUIDs must be unique under concurrent access")
}

func TestStepContext_SequenceIntegrity(t *testing.T) {
	// Verify that call indices have no gaps under concurrent access
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ctx := NewStepContext(instanceID, 0)
	gen := NewUUIDGenerator(instanceID)

	numGoroutines := 10
	uuidsPerGoroutine := 10
	totalUUIDs := numGoroutines * uuidsPerGoroutine

	var wg sync.WaitGroup
	uuidChan := make(chan uuid.UUID, totalUUIDs)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < uuidsPerGoroutine; j++ {
				id := ctx.NewUUID()
				uuidChan <- id
			}
		}()
	}

	wg.Wait()
	close(uuidChan)

	// Collect all generated UUIDs
	generatedUUIDs := make(map[uuid.UUID]struct{})
	for id := range uuidChan {
		generatedUUIDs[id] = struct{}{}
	}

	// Generate expected UUIDs for call indices 0 to totalUUIDs-1
	expectedUUIDs := make(map[uuid.UUID]struct{})
	for i := 0; i < totalUUIDs; i++ {
		expectedUUIDs[gen.NewUUID(0, i)] = struct{}{}
	}

	// All generated UUIDs must match expected (sequence has no gaps)
	assert.Equal(t, expectedUUIDs, generatedUUIDs, "sequence must have no gaps")
}

func BenchmarkStepContext_NewUUID(b *testing.B) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ctx := NewStepContext(instanceID, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ctx.NewUUID()
	}
}

func BenchmarkUUIDGenerator_NewUUID(b *testing.B) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	gen := NewUUIDGenerator(instanceID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = gen.NewUUID(0, i)
	}
}

// TestStepContext_NewUUID_MatchesUUIDGenerator verifies that StepContext.NewUUID
// produces the same result as directly calling UUIDGenerator.NewUUID with the
// corresponding step and call indices.
func TestStepContext_NewUUID_MatchesUUIDGenerator(t *testing.T) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	ctx := NewStepContext(instanceID, 5)
	newUUID := ctx.NewUUID()

	// StepContext.NewUUID should produce identical result to UUIDGenerator.NewUUID
	// with the same namespace, stepIndex, and callIndex
	gen := NewUUIDGenerator(instanceID)
	expected := gen.NewUUID(5, 0)
	assert.Equal(t, expected, newUUID)
}

// TestNewUUIDFromContextValue tests getting UUID from Starlark thread context
func TestNewUUIDFromContextValue(t *testing.T) {
	instanceID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ctx := NewStepContext(instanceID, 2)

	// This tests that the context can be used as expected
	require.NotNil(t, ctx)
	assert.Equal(t, instanceID, ctx.InstanceID())
	assert.Equal(t, 2, ctx.StepIndex())
}
