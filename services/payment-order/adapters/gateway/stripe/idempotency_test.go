package stripe

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestIdempotencyKey_Deterministic(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	op := "payment_intent"

	key1 := IdempotencyKey(id, op)
	key2 := IdempotencyKey(id, op)

	assert.Equal(t, key1, key2, "same inputs must produce same key")
}

func TestIdempotencyKey_DifferentOperations(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	keyPI := IdempotencyKey(id, "payment_intent")
	keySI := IdempotencyKey(id, "setup_intent")

	assert.NotEqual(t, keyPI, keySI, "different operations must produce different keys")
}

func TestIdempotencyKey_DifferentOrders(t *testing.T) {
	id1 := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	id2 := uuid.MustParse("550e8400-e29b-41d4-a716-446655440001")

	key1 := IdempotencyKey(id1, "payment_intent")
	key2 := IdempotencyKey(id2, "payment_intent")

	assert.NotEqual(t, key1, key2, "different order IDs must produce different keys")
}

func TestIdempotencyKey_HasPrefix(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	key := IdempotencyKey(id, "payment_intent")

	assert.Contains(t, key, "mdn:", "key must have mdn: prefix")
}

func TestIdempotencyKey_ConsistentLength(t *testing.T) {
	id1 := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	id2 := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")

	key1 := IdempotencyKey(id1, "payment_intent")
	key2 := IdempotencyKey(id2, "refund")

	assert.Equal(t, len(key1), len(key2), "keys should have consistent length")
	// "mdn:" (4) + 32 hex chars = 36
	assert.Len(t, key1, 36)
}
