package primitives

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Valid UUID v4 for testing
const validUUID = "550e8400-e29b-41d4-a716-446655440000"

// Test table for invalid UUIDs
var invalidUUIDs = []struct {
	name  string
	input string
	err   error
}{
	{"empty string", "", ErrEmptyID},
	{"not a UUID", "not-a-uuid", ErrInvalidIDFormat},
	{"UUID v1", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", ErrInvalidIDFormat},
	{"wrong version nibble", "550e8400-e29b-31d4-a716-446655440000", ErrInvalidIDFormat},
	{"wrong variant nibble", "550e8400-e29b-41d4-0716-446655440000", ErrInvalidIDFormat},
	{"too short", "550e8400-e29b-41d4-a716", ErrInvalidIDFormat},
	{"too long", "550e8400-e29b-41d4-a716-4466554400000000", ErrInvalidIDFormat},
	{"missing dashes", "550e8400e29b41d4a716446655440000", ErrInvalidIDFormat},
	{"extra dashes", "550e-8400-e29b-41d4-a716-446655440000", ErrInvalidIDFormat},
	{"invalid characters", "550e8400-e29b-41d4-a716-44665544000g", ErrInvalidIDFormat},
}

func TestAccountID(t *testing.T) {
	t.Run("NewAccountID accepts valid UUID v4", func(t *testing.T) {
		result := NewAccountID(validUUID)

		assert.True(t, result.IsOk())
		assert.Equal(t, AccountID(validUUID), result.MustGet())
	})

	t.Run("NewAccountID accepts uppercase UUID", func(t *testing.T) {
		result := NewAccountID("550E8400-E29B-41D4-A716-446655440000")

		assert.True(t, result.IsOk())
	})

	t.Run("NewAccountID rejects invalid formats", func(t *testing.T) {
		for _, tc := range invalidUUIDs {
			t.Run(tc.name, func(t *testing.T) {
				result := NewAccountID(tc.input)

				assert.True(t, result.IsError())
				assert.ErrorIs(t, result.Error(), tc.err)
			})
		}
	})

	t.Run("String returns underlying value", func(t *testing.T) {
		id := AccountID(validUUID)
		assert.Equal(t, validUUID, id.String())
	})

	t.Run("JSON marshal produces quoted string", func(t *testing.T) {
		id := AccountID(validUUID)

		data, err := json.Marshal(id)

		require.NoError(t, err)
		assert.Equal(t, `"`+validUUID+`"`, string(data))
	})

	t.Run("JSON unmarshal accepts valid UUID", func(t *testing.T) {
		var id AccountID
		err := json.Unmarshal([]byte(`"`+validUUID+`"`), &id)

		require.NoError(t, err)
		assert.Equal(t, AccountID(validUUID), id)
	})

	t.Run("JSON unmarshal rejects invalid UUID", func(t *testing.T) {
		var id AccountID
		err := json.Unmarshal([]byte(`"not-valid"`), &id)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidIDFormat)
	})

	t.Run("JSON roundtrip preserves value", func(t *testing.T) {
		original := AccountID(validUUID)

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored AccountID
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, original, restored)
	})
}

func TestCustomerID(t *testing.T) {
	t.Run("NewCustomerID accepts valid UUID v4", func(t *testing.T) {
		result := NewCustomerID(validUUID)

		assert.True(t, result.IsOk())
		assert.Equal(t, CustomerID(validUUID), result.MustGet())
	})

	t.Run("NewCustomerID rejects invalid formats", func(t *testing.T) {
		for _, tc := range invalidUUIDs {
			t.Run(tc.name, func(t *testing.T) {
				result := NewCustomerID(tc.input)

				assert.True(t, result.IsError())
				assert.ErrorIs(t, result.Error(), tc.err)
			})
		}
	})

	t.Run("String returns underlying value", func(t *testing.T) {
		id := CustomerID(validUUID)
		assert.Equal(t, validUUID, id.String())
	})

	t.Run("JSON marshal/unmarshal roundtrip", func(t *testing.T) {
		original := CustomerID(validUUID)

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored CustomerID
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, original, restored)
	})

	t.Run("JSON unmarshal rejects invalid UUID", func(t *testing.T) {
		var id CustomerID
		err := json.Unmarshal([]byte(`""`), &id)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyID)
	})
}

func TestTransactionID(t *testing.T) {
	t.Run("NewTransactionID accepts valid UUID v4", func(t *testing.T) {
		result := NewTransactionID(validUUID)

		assert.True(t, result.IsOk())
		assert.Equal(t, TransactionID(validUUID), result.MustGet())
	})

	t.Run("NewTransactionID rejects invalid formats", func(t *testing.T) {
		for _, tc := range invalidUUIDs {
			t.Run(tc.name, func(t *testing.T) {
				result := NewTransactionID(tc.input)

				assert.True(t, result.IsError())
				assert.ErrorIs(t, result.Error(), tc.err)
			})
		}
	})

	t.Run("String returns underlying value", func(t *testing.T) {
		id := TransactionID(validUUID)
		assert.Equal(t, validUUID, id.String())
	})

	t.Run("JSON marshal/unmarshal roundtrip", func(t *testing.T) {
		original := TransactionID(validUUID)

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored TransactionID
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, original, restored)
	})
}

func TestPostingID(t *testing.T) {
	t.Run("NewPostingID accepts valid UUID v4", func(t *testing.T) {
		result := NewPostingID(validUUID)

		assert.True(t, result.IsOk())
		assert.Equal(t, PostingID(validUUID), result.MustGet())
	})

	t.Run("NewPostingID rejects invalid formats", func(t *testing.T) {
		for _, tc := range invalidUUIDs {
			t.Run(tc.name, func(t *testing.T) {
				result := NewPostingID(tc.input)

				assert.True(t, result.IsError())
				assert.ErrorIs(t, result.Error(), tc.err)
			})
		}
	})

	t.Run("String returns underlying value", func(t *testing.T) {
		id := PostingID(validUUID)
		assert.Equal(t, validUUID, id.String())
	})

	t.Run("JSON marshal/unmarshal roundtrip", func(t *testing.T) {
		original := PostingID(validUUID)

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored PostingID
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, original, restored)
	})
}

func TestLedgerID(t *testing.T) {
	t.Run("NewLedgerID accepts valid UUID v4", func(t *testing.T) {
		result := NewLedgerID(validUUID)

		assert.True(t, result.IsOk())
		assert.Equal(t, LedgerID(validUUID), result.MustGet())
	})

	t.Run("NewLedgerID rejects invalid formats", func(t *testing.T) {
		for _, tc := range invalidUUIDs {
			t.Run(tc.name, func(t *testing.T) {
				result := NewLedgerID(tc.input)

				assert.True(t, result.IsError())
				assert.ErrorIs(t, result.Error(), tc.err)
			})
		}
	})

	t.Run("String returns underlying value", func(t *testing.T) {
		id := LedgerID(validUUID)
		assert.Equal(t, validUUID, id.String())
	})

	t.Run("JSON marshal/unmarshal roundtrip", func(t *testing.T) {
		original := LedgerID(validUUID)

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored LedgerID
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, original, restored)
	})
}

// TestTypeSafety verifies that different ID types cannot be used interchangeably.
// This is a compile-time guarantee, but we document it with a test.
func TestTypeSafety(t *testing.T) {
	t.Run("different ID types are distinct", func(t *testing.T) {
		// These are the same UUID string but different types
		accountID := AccountID(validUUID)
		customerID := CustomerID(validUUID)

		// The string values are equal
		assert.Equal(t, accountID.String(), customerID.String())

		// But the types are different (this is enforced at compile time)
		// The following would not compile:
		// var a AccountID = customerID  // type error
		// funcTakingAccountID(customerID)  // type error
	})
}

// TestStructEmbedding verifies JSON serialization works when IDs are embedded in structs.
func TestStructEmbedding(t *testing.T) {
	type Account struct {
		ID         AccountID  `json:"id"`
		CustomerID CustomerID `json:"customer_id"`
		Name       string     `json:"name"`
	}

	t.Run("struct with ID fields serializes correctly", func(t *testing.T) {
		acc := Account{
			ID:         AccountID(validUUID),
			CustomerID: CustomerID("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
			Name:       "Test Account",
		}

		data, err := json.Marshal(acc)
		require.NoError(t, err)

		var restored Account
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, acc.ID, restored.ID)
		assert.Equal(t, acc.CustomerID, restored.CustomerID)
		assert.Equal(t, acc.Name, restored.Name)
	})

	t.Run("struct with invalid ID fails to unmarshal", func(t *testing.T) {
		jsonData := `{"id":"invalid","customer_id":"also-invalid","name":"Test"}`

		var acc Account
		err := json.Unmarshal([]byte(jsonData), &acc)

		assert.Error(t, err)
	})
}
