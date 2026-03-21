package valuation

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimateCELCost(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		expected int
	}{
		{"empty expression returns 1", "", 1},
		{"short expression", "a > 0", 1},
		{"medium expression", "amount > 0 && amount <= 1000000 && currency == 'GBP'", 5},
		{"very long expression clamps at 9999", string(make([]byte, 200000)), 9999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, estimateCELCost(tt.expr))
		})
	}
}

func TestComputeHash(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		h1 := computeHash("hello")
		h2 := computeHash("hello")
		assert.Equal(t, h1, h2)
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		h1 := computeHash("hello")
		h2 := computeHash("world")
		assert.NotEqual(t, h1, h2)
	})

	t.Run("returns 64 char hex string", func(t *testing.T) {
		h := computeHash("test")
		assert.Len(t, h, 64) // SHA-256 = 32 bytes = 64 hex chars
	})
}

func TestNullString(t *testing.T) {
	t.Run("empty string returns invalid", func(t *testing.T) {
		ns := nullString("")
		assert.Equal(t, sql.NullString{Valid: false}, ns)
	})

	t.Run("non-empty string returns valid", func(t *testing.T) {
		ns := nullString("hello")
		assert.Equal(t, sql.NullString{String: "hello", Valid: true}, ns)
	})
}

func TestBuildDryRunInput(t *testing.T) {
	t.Run("nil inputs", func(t *testing.T) {
		result := buildDryRunInput(nil)
		assert.NotNil(t, result["attributes"])
		assert.Equal(t, "", result["amount"])
		assert.Equal(t, "", result["source"])
	})

	t.Run("with amount", func(t *testing.T) {
		result := buildDryRunInput(map[string]string{"amount": "100.50"})
		assert.Equal(t, "100.50", result["amount"])
	})

	t.Run("without amount", func(t *testing.T) {
		result := buildDryRunInput(map[string]string{"currency": "GBP"})
		assert.Equal(t, "", result["amount"])
	})
}

func TestFailedDryRun(t *testing.T) {
	result := failedDryRun(42, "compilation error")
	assert.False(t, result.Success)
	assert.Equal(t, 42, result.EstimatedCost)
	assert.Equal(t, []string{"compilation error"}, result.Errors)
}
