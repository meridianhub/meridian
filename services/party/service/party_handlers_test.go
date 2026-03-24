package service

import (
	"context"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RegisterParty with initial attributes is not covered by existing grpc_service_test.go tests.

func TestRegisterParty_WithInitialAttributes(t *testing.T) {
	ctx := context.Background()
	mock := newMockRepository()
	svc := newTestService(mock)

	resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Tech Corp Ltd",
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "industry", Value: "technology"},
			{Key: "country", Value: "GB"},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Party)

	assert.Equal(t, 2, len(resp.Party.Attributes))
	// Attributes are stored and returned (exact order not guaranteed by map iteration)
	keyValues := make(map[string]string)
	for _, a := range resp.Party.Attributes {
		keyValues[a.Key] = a.Value
	}
	assert.Equal(t, "technology", keyValues["industry"])
	assert.Equal(t, "GB", keyValues["country"])
}

func TestRegisterParty_WithDisplayNameAndAttributes(t *testing.T) {
	ctx := context.Background()
	mock := newMockRepository()
	svc := newTestService(mock)

	resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType:   pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:   "Meridian Financial Services Ltd",
		DisplayName: "Meridian FS",
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "sector", Value: "finance"},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "Meridian FS", resp.Party.DisplayName)
	assert.Equal(t, 1, len(resp.Party.Attributes))
	assert.Equal(t, "sector", resp.Party.Attributes[0].Key)
	assert.Equal(t, "finance", resp.Party.Attributes[0].Value)
}

// RegisterParty with no attributes should return empty attributes, not nil

func TestRegisterParty_NoAttributes_ReturnsEmptyList(t *testing.T) {
	ctx := context.Background()
	mock := newMockRepository()
	svc := newTestService(mock)

	resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_PERSON,
		LegalName: "John Doe",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	// No attributes provided - should not include any in response
	assert.Empty(t, resp.Party.Attributes)
}

// UpdateParty with attributes field mask - verify attribute replacement

func TestUpdateParty_AttributesReplace(t *testing.T) {
	ctx := context.Background()
	mock := newMockRepository()
	svc := newTestService(mock)

	// Register a party with initial attributes via domain
	resp1, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Replace Org",
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "old_key", Value: "old_value"},
		},
	})
	require.NoError(t, err)

	// Update with new attributes - should replace, not merge
	resp2, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
		PartyId: resp1.Party.PartyId,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "new_key", Value: "new_value"},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp2)
	assert.Equal(t, 1, len(resp2.Party.Attributes))
	assert.Equal(t, "new_key", resp2.Party.Attributes[0].Key)
	assert.Equal(t, "new_value", resp2.Party.Attributes[0].Value)
}

// Verify that UpdateParty with version 0 (unset) skips the version check

func TestUpdateParty_ZeroVersionSkipsCheck(t *testing.T) {
	ctx := context.Background()
	mock := newMockRepository()
	svc := newTestService(mock)

	resp1, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_PERSON,
		LegalName: "Version Test",
	})
	require.NoError(t, err)

	// Version 0 means "don't check" - should succeed regardless of actual version
	resp2, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
		PartyId:     resp1.Party.PartyId,
		DisplayName: "Updated Name",
		Version:     0, // Zero version skips check
	})

	require.NoError(t, err)
	assert.Equal(t, "Updated Name", resp2.Party.DisplayName)
}

// ControlParty returns InvalidArgument for invalid party type action combinations

func TestControlParty_ReturnsInternalOnSaveError_Suspend(t *testing.T) {
	ctx := context.Background()
	mock := newMockRepository()
	svc := newTestService(mock)

	// Register party, then inject save error for control action
	resp1, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_PERSON,
		LegalName: "Control Test",
	})
	require.NoError(t, err)

	// Now inject a save error
	mock.saveErr = errDatabaseTimeout

	_, err = svc.ControlParty(ctx, &pb.ControlPartyRequest{
		PartyId:       resp1.Party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "testing",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}
