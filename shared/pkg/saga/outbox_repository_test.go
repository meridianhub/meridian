package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewGormTxContextWithOutbox(t *testing.T) {
	ctx := NewGormTxContextWithOutbox(nil, "test-service")
	assert.NotNil(t, ctx)
	assert.Equal(t, "test-service", ctx.serviceName)
}

func TestNewGormTransactionalRepositoryWithOutbox(t *testing.T) {
	repo := NewGormTransactionalRepositoryWithOutbox(nil, "test-service")
	assert.NotNil(t, repo)
	assert.Equal(t, "test-service", repo.serviceName)
}

func TestGormTxContextWithOutbox_implements_interface(t *testing.T) {
	// Compile-time check already exists in source, but verify at test time
	var _ TxContextWithOutbox = (*GormTxContextWithOutbox)(nil)
}

func TestGormTransactionalRepositoryWithOutbox_implements_interface(t *testing.T) {
	var _ TransactionalRepositoryWithOutbox = (*GormTransactionalRepositoryWithOutbox)(nil)
}
