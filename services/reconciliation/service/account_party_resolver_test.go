package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// mockCurrentAccountClient is a test double for the CurrentAccountServiceClient.
type mockCurrentAccountClient struct {
	currentaccountv1.CurrentAccountServiceClient
	resp *currentaccountv1.RetrieveCurrentAccountResponse
	err  error
}

func (m *mockCurrentAccountClient) RetrieveCurrentAccount(
	_ context.Context,
	_ *currentaccountv1.RetrieveCurrentAccountRequest,
	_ ...grpc.CallOption,
) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
	return m.resp, m.err
}

func TestGRPCAccountPartyResolver_ResolvePartyID_Success(t *testing.T) {
	partyID := uuid.New()
	client := &mockCurrentAccountClient{
		resp: &currentaccountv1.RetrieveCurrentAccountResponse{
			Facility: &currentaccountv1.CurrentAccountFacility{
				AccountId: "ACC-001",
				PartyId:   partyID.String(),
			},
		},
	}

	resolver := NewGRPCAccountPartyResolver(client)
	result, err := resolver.ResolvePartyID(context.Background(), "ACC-001")

	require.NoError(t, err)
	assert.Equal(t, partyID, result)
}

func TestGRPCAccountPartyResolver_ResolvePartyID_GRPCError(t *testing.T) {
	client := &mockCurrentAccountClient{
		err: errors.New("connection refused"),
	}

	resolver := NewGRPCAccountPartyResolver(client)
	result, err := resolver.ResolvePartyID(context.Background(), "ACC-001")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to retrieve account ACC-001")
	assert.Equal(t, uuid.Nil, result)
}

func TestGRPCAccountPartyResolver_ResolvePartyID_NilFacility(t *testing.T) {
	client := &mockCurrentAccountClient{
		resp: &currentaccountv1.RetrieveCurrentAccountResponse{
			Facility: nil,
		},
	}

	resolver := NewGRPCAccountPartyResolver(client)
	result, err := resolver.ResolvePartyID(context.Background(), "ACC-001")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoFacilityReturned)
	assert.Equal(t, uuid.Nil, result)
}

func TestGRPCAccountPartyResolver_ResolvePartyID_EmptyPartyID(t *testing.T) {
	client := &mockCurrentAccountClient{
		resp: &currentaccountv1.RetrieveCurrentAccountResponse{
			Facility: &currentaccountv1.CurrentAccountFacility{
				AccountId: "ACC-001",
				PartyId:   "",
			},
		},
	}

	resolver := NewGRPCAccountPartyResolver(client)
	result, err := resolver.ResolvePartyID(context.Background(), "ACC-001")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoPartyID)
	assert.Equal(t, uuid.Nil, result)
}

func TestGRPCAccountPartyResolver_ResolvePartyID_InvalidPartyID(t *testing.T) {
	client := &mockCurrentAccountClient{
		resp: &currentaccountv1.RetrieveCurrentAccountResponse{
			Facility: &currentaccountv1.CurrentAccountFacility{
				AccountId: "ACC-001",
				PartyId:   "not-a-uuid",
			},
		},
	}

	resolver := NewGRPCAccountPartyResolver(client)
	result, err := resolver.ResolvePartyID(context.Background(), "ACC-001")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid party_id")
	assert.Equal(t, uuid.Nil, result)
}
