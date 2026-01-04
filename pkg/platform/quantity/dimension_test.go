package quantity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meridianhub/meridian/pkg/platform/quantity"
)

func TestMonetary_String(t *testing.T) {
	m := quantity.Monetary{}
	assert.Equal(t, "Monetary", m.String())
}

func TestCommodity_String(t *testing.T) {
	c := quantity.Commodity{}
	assert.Equal(t, "Commodity", c.String())
}

func TestMonetary_Validate(t *testing.T) {
	m := quantity.Monetary{}
	assert.NoError(t, m.Validate())
}

func TestCommodity_Validate(t *testing.T) {
	c := quantity.Commodity{}
	assert.NoError(t, c.Validate())
}

func TestDimensions_ImplementDimensionInterface(_ *testing.T) {
	// Verify both types implement the Dimension interface.
	// This is a compile-time check - if either type doesn't implement
	// the interface, this file won't compile.
	var _ quantity.Dimension = quantity.Monetary{}
	var _ quantity.Dimension = quantity.Commodity{}
}

func TestDimensions_AreDistinctTypes(t *testing.T) {
	// This test documents the phantom type pattern.
	// The compile-time type safety is demonstrated in TestQuantity_TypeSafety below.
	// At runtime, we can verify they are different types via reflection or
	// simply by their String() values.
	m := quantity.Monetary{}
	c := quantity.Commodity{}
	assert.NotEqual(t, m.String(), c.String(), "Monetary and Commodity should have distinct String() values")
}

// TestQuantity_TypeSafety documents the compile-time type safety guarantee.
// The following code would NOT compile if uncommented, proving that
// Quantity[Monetary] and Quantity[Commodity] are distinct types:
//
//	func wouldNotCompile() {
//		var m quantity.Quantity[quantity.Monetary]
//		var c quantity.Quantity[quantity.Commodity]
//		m = c // compile error: cannot use c (variable of type Quantity[Commodity]) as Quantity[Monetary] value
//	}
//
// This test exists to document the safety guarantee - the actual compile-time
// check is done by the Go compiler itself.
func TestQuantity_TypeSafety(t *testing.T) {
	// This test verifies that the type system enforces dimension separation.
	// We can't test compile-time errors at runtime, so we document the pattern.
	t.Log("Compile-time type safety: Quantity[Monetary] and Quantity[Commodity] are distinct types")
	t.Log("Attempting to assign between them would result in a compile error")
}
