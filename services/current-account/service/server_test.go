package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServiceWithExistingClients_NilRepo(t *testing.T) {
	_, err := NewServiceWithExistingClients(
		nil,  // repo
		nil,  // lienRepo
		nil,  // withdrawalRepo
		nil,  // outboxRepo
		nil,  // db
		nil,  // posKeepingClient
		nil,  // finAcctClient
		nil,  // partyClient
		nil,  // accountConfig
		nil,  // idempotencyService
		nil,  // logger
		nil,  // tracer
		nil,  // accountResolver
		nil,  // fungibilityValidator
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRepositoryNil)
}
