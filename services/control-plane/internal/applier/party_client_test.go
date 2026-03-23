package applier

import (
	"context"
	"net"
	"testing"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestParseExternalReferenceType_Empty(t *testing.T) {
	refType, err := parseExternalReferenceType("")
	require.NoError(t, err)
	assert.Equal(t, partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED, refType)
}

func TestParseExternalReferenceType_Known(t *testing.T) {
	refType, err := parseExternalReferenceType("EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE")
	require.NoError(t, err)
	assert.Equal(t, partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE, refType)
}

func TestParseExternalReferenceType_Unknown(t *testing.T) {
	_, err := parseExternalReferenceType("BOGUS")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownExternalReferenceType)
}

func TestParseExternalReferenceType_Stripped(t *testing.T) {
	refType, err := parseExternalReferenceType("COMPANIES_HOUSE")
	require.NoError(t, err)
	assert.Equal(t, partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE, refType)
}

func TestNewPartyClient(t *testing.T) {
	c := NewPartyClient(nil)
	assert.NotNil(t, c)
}

// ─── Fake gRPC servers ───────────────────────────────────────────────────────

type fakePartyServer struct {
	partyv1.UnimplementedPartyServiceServer
	registerFn func(context.Context, *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error)
}

func (f *fakePartyServer) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
	if f.registerFn != nil {
		return f.registerFn(ctx, req)
	}
	return &partyv1.RegisterPartyResponse{
		Party: &partyv1.Party{
			PartyId:   "party-uuid-1",
			LegalName: req.LegalName,
			Status:    partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
		},
	}, nil
}

func newPartyTestServer(t *testing.T, srv *fakePartyServer) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	partyv1.RegisterPartyServiceServer(s, srv)

	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.GracefulStop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestRegisterOrganization_Success(t *testing.T) {
	conn := newPartyTestServer(t, &fakePartyServer{})
	client := NewPartyClient(conn)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	result, err := client.RegisterOrganization(ctx, map[string]any{
		"legal_name":   "Acme Corp",
		"display_name": "Acme",
	})

	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, "party-uuid-1", m["party_id"])
	assert.Equal(t, "Acme Corp", m["legal_name"])
}

func TestRegisterOrganization_AlreadyExists_TreatedAsSuccess(t *testing.T) {
	srv := &fakePartyServer{
		registerFn: func(_ context.Context, _ *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "party with external reference of type LEI already exists")
		},
	}
	conn := newPartyTestServer(t, srv)
	client := NewPartyClient(conn)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	result, err := client.RegisterOrganization(ctx, map[string]any{
		"legal_name":             "Acme Corp",
		"external_reference":     "123456",
		"external_reference_type": "LEI",
	})

	require.NoError(t, err, "AlreadyExists should be treated as idempotent success")
	m := result.(map[string]any)
	assert.Equal(t, "Acme Corp", m["legal_name"])
	assert.Equal(t, "PARTY_STATUS_ACTIVE", m["status"])
}

func TestRegisterOrganization_OtherError_Propagated(t *testing.T) {
	srv := &fakePartyServer{
		registerFn: func(_ context.Context, _ *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
			return nil, status.Error(codes.Internal, "database unavailable")
		},
	}
	conn := newPartyTestServer(t, srv)
	client := NewPartyClient(conn)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	_, err := client.RegisterOrganization(ctx, map[string]any{
		"legal_name": "Acme Corp",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")
}
