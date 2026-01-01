package main

import (
	"context"
	"testing"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/current-account/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockPartyClient is a mock for the party client for testing.
type mockPartyClient struct {
	retrievePartyResp *partyv1.RetrievePartyResponse
	retrievePartyErr  error
}

// RetrieveParty mocks the RetrieveParty method.
func (m *mockPartyClient) RetrieveParty(_ context.Context, _ *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
	return m.retrievePartyResp, m.retrievePartyErr
}

// Close mocks the Close method.
func (m *mockPartyClient) Close() error {
	return nil
}

// testablePartyWrapper wraps a mockPartyClient for testing.
// This avoids needing to mock the full partyclient.Client struct.
type testablePartyWrapper struct {
	mock *mockPartyClient
}

// ValidateParty delegates to the mock.
func (w *testablePartyWrapper) ValidateParty(ctx context.Context, partyID string) error {
	party, err := w.GetParty(ctx, partyID)
	if err != nil {
		return err
	}

	if party.Status != partyv1.PartyStatus_PARTY_STATUS_ACTIVE {
		return service.ErrPartyNotActive
	}

	return nil
}

// GetParty delegates to the mock.
func (w *testablePartyWrapper) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error) {
	resp, err := w.mock.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{PartyId: partyID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, service.ErrPartyNotFound
		}
		return nil, err
	}
	return resp.Party, nil
}

// Close delegates to the mock.
func (w *testablePartyWrapper) Close() error {
	return w.mock.Close()
}

func TestPartyWrapper_ValidateParty_ReturnsNilForActiveParty(t *testing.T) {
	mock := &mockPartyClient{
		retrievePartyResp: &partyv1.RetrievePartyResponse{
			Party: &partyv1.Party{
				PartyId: "party-123",
				Status:  partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
			},
		},
	}
	wrapper := &testablePartyWrapper{mock: mock}

	err := wrapper.ValidateParty(context.Background(), "party-123")

	assert.NoError(t, err)
}

func TestPartyWrapper_ValidateParty_ReturnsErrPartyNotActiveForInactiveParty(t *testing.T) {
	testCases := []struct {
		name   string
		status partyv1.PartyStatus
	}{
		{"RESTRICTED", partyv1.PartyStatus_PARTY_STATUS_RESTRICTED},
		{"SUSPENDED", partyv1.PartyStatus_PARTY_STATUS_SUSPENDED},
		{"TERMINATED", partyv1.PartyStatus_PARTY_STATUS_TERMINATED},
		{"UNSPECIFIED", partyv1.PartyStatus_PARTY_STATUS_UNSPECIFIED},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockPartyClient{
				retrievePartyResp: &partyv1.RetrievePartyResponse{
					Party: &partyv1.Party{
						PartyId: "party-123",
						Status:  tc.status,
					},
				},
			}
			wrapper := &testablePartyWrapper{mock: mock}

			err := wrapper.ValidateParty(context.Background(), "party-123")

			require.Error(t, err)
			assert.ErrorIs(t, err, service.ErrPartyNotActive)
		})
	}
}

func TestPartyWrapper_GetParty_ReturnsErrPartyNotFoundForNotFoundStatus(t *testing.T) {
	mock := &mockPartyClient{
		retrievePartyErr: status.Error(codes.NotFound, "party not found"),
	}
	wrapper := &testablePartyWrapper{mock: mock}

	party, err := wrapper.GetParty(context.Background(), "nonexistent-party")

	assert.Nil(t, party)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrPartyNotFound)
}

func TestPartyWrapper_GetParty_ReturnsPartyForExistingParty(t *testing.T) {
	expectedParty := &partyv1.Party{
		PartyId:   "party-123",
		LegalName: "Test Party",
		Status:    partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}
	mock := &mockPartyClient{
		retrievePartyResp: &partyv1.RetrievePartyResponse{
			Party: expectedParty,
		},
	}
	wrapper := &testablePartyWrapper{mock: mock}

	party, err := wrapper.GetParty(context.Background(), "party-123")

	require.NoError(t, err)
	assert.Equal(t, expectedParty, party)
}

func TestPartyWrapper_GetParty_PassesThroughOtherErrors(t *testing.T) {
	mock := &mockPartyClient{
		retrievePartyErr: status.Error(codes.Internal, "internal server error"),
	}
	wrapper := &testablePartyWrapper{mock: mock}

	party, err := wrapper.GetParty(context.Background(), "party-123")

	assert.Nil(t, party)
	require.Error(t, err)
	// Should not be ErrPartyNotFound
	assert.NotErrorIs(t, err, service.ErrPartyNotFound)
	// Should be the original gRPC error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestPartyWrapper_ValidateParty_ReturnsErrPartyNotFoundWhenPartyNotFound(t *testing.T) {
	mock := &mockPartyClient{
		retrievePartyErr: status.Error(codes.NotFound, "party not found"),
	}
	wrapper := &testablePartyWrapper{mock: mock}

	err := wrapper.ValidateParty(context.Background(), "nonexistent-party")

	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrPartyNotFound)
}
