package main

import (
	"context"
	"net"
	"testing"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// fakePartyService implements PartyServiceServer for testing email resolution.
type fakePartyService struct {
	partyv1.UnimplementedPartyServiceServer
	parties map[string]*partyv1.Party
}

func (f *fakePartyService) RetrieveParty(_ context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
	p, ok := f.parties[req.PartyId]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "party %s not found", req.PartyId)
	}
	return &partyv1.RetrievePartyResponse{Party: p}, nil
}

func newTestPartyServer(t *testing.T, parties map[string]*partyv1.Party) *grpc.ClientConn {
	t.Helper()

	srv := grpc.NewServer()
	partyv1.RegisterPartyServiceServer(srv, &fakePartyService{parties: parties})

	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestGRPCPartyEmailResolver_ResolveEmail(t *testing.T) {
	parties := map[string]*partyv1.Party{
		"party-1": {
			PartyId:   "party-1",
			LegalName: "Alice",
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "email", Value: "alice@example.com"},
				{Key: "phone", Value: "+44123456"},
			},
		},
		"party-no-email": {
			PartyId:   "party-no-email",
			LegalName: "Bob",
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "phone", Value: "+44999999"},
			},
		},
		"party-empty-email": {
			PartyId:   "party-empty-email",
			LegalName: "Charlie",
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "email", Value: ""},
			},
		},
	}

	conn := newTestPartyServer(t, parties)
	resolver := newGRPCPartyEmailResolver(conn)
	ctx := context.Background()

	t.Run("resolves email from party attributes", func(t *testing.T) {
		email, err := resolver.ResolveEmail(ctx, "party-1")
		require.NoError(t, err)
		assert.Equal(t, "alice@example.com", email)
	})

	t.Run("error when party not found", func(t *testing.T) {
		_, err := resolver.ResolveEmail(ctx, "nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "retrieve party nonexistent")
	})

	t.Run("error when no email attribute", func(t *testing.T) {
		_, err := resolver.ResolveEmail(ctx, "party-no-email")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPartyEmailNotFound)
	})

	t.Run("error when empty email attribute", func(t *testing.T) {
		_, err := resolver.ResolveEmail(ctx, "party-empty-email")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPartyEmailEmpty)
	})
}
