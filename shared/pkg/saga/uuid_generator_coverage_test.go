package saga

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestStepContext_CallIndex(t *testing.T) {
	instanceID := uuid.New()
	ctx := NewStepContext(instanceID, 3)

	// Initially 0
	assert.Equal(t, int32(0), ctx.CallIndex())

	// Generate a UUID - call index should increment
	_ = ctx.NewUUID()
	assert.Equal(t, int32(1), ctx.CallIndex())

	// Generate another
	_ = ctx.NewUUID()
	assert.Equal(t, int32(2), ctx.CallIndex())
}
