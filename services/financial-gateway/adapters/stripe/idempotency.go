package stripe

import (
	"crypto/sha256"
	"encoding/hex"
)

// IdempotencyKey generates a deterministic idempotency key from a payment order ID
// and an operation suffix. The same (paymentOrderID, operation) pair always produces
// the same key, ensuring safe retries without duplicate Stripe charges.
//
// Format: "mdn:<sha256-prefix>" where the hash is over "paymentOrderID:operation".
//
// Example operations: "payment_intent", "setup_intent", "refund", "capture".
func IdempotencyKey(paymentOrderID string, operation string) string {
	data := paymentOrderID + ":" + operation
	hash := sha256.Sum256([]byte(data))
	return "mdn:" + hex.EncodeToString(hash[:16])
}
