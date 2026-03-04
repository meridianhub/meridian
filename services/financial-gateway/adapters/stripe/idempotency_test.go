package stripe

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIdempotencyKey_Deterministic(t *testing.T) {
	key1 := IdempotencyKey("11111111-1111-1111-1111-111111111111", "payment_intent")
	key2 := IdempotencyKey("11111111-1111-1111-1111-111111111111", "payment_intent")
	assert.Equal(t, key1, key2, "same inputs must produce same key")
}

func TestIdempotencyKey_DifferentOperations(t *testing.T) {
	key1 := IdempotencyKey("11111111-1111-1111-1111-111111111111", "payment_intent")
	key2 := IdempotencyKey("11111111-1111-1111-1111-111111111111", "refund")
	assert.NotEqual(t, key1, key2, "different operations must produce different keys")
}

func TestIdempotencyKey_DifferentOrders(t *testing.T) {
	key1 := IdempotencyKey("11111111-1111-1111-1111-111111111111", "payment_intent")
	key2 := IdempotencyKey("22222222-2222-2222-2222-222222222222", "payment_intent")
	assert.NotEqual(t, key1, key2, "different orders must produce different keys")
}

func TestIdempotencyKey_Format(t *testing.T) {
	key := IdempotencyKey("11111111-1111-1111-1111-111111111111", "payment_intent")
	assert.Contains(t, key, "mdn:", "key must have mdn: prefix")
	assert.Len(t, key, 4+32, "key must be prefix (4 chars) + hex (32 chars)")
}
