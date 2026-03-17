package applier

import (
	"context"
	"net"
	"testing"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Compile-time interface satisfaction check.
var _ InternalAccountService = (*InternalAccountClient)(nil)

// ─── Mock gRPC server ──────────────────────────────────────────────────────

type fakeInternalAccountServer struct {
	internalaccountv1.UnimplementedInternalAccountServiceServer
}

func (f *fakeInternalAccountServer) InitiateInternalAccount(_ context.Context, req *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error) {
	return &internalaccountv1.InitiateInternalAccountResponse{
		AccountId: "ia-uuid-1",
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountCode: req.AccountCode,
		},
	}, nil
}

// ─── Test setup ────────────────────────────────────────────────────────────

func newInternalAccountTestServer(t *testing.T) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	internalaccountv1.RegisterInternalAccountServiceServer(srv, &fakeInternalAccountServer{})

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// ─── Tests ─────────────────────────────────────────────────────────────────

func TestInternalAccountClient_InitiateAccount(t *testing.T) {
	conn := newInternalAccountTestServer(t)
	client := NewInternalAccountClient(conn)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	result, err := client.InitiateAccount(ctx, map[string]any{
		"account_code":    "CLEARING_GBP",
		"name":            "GBP Clearing",
		"account_type":    "CLEARING",
		"instrument_code": "GBP",
		"description":     "Clearing account for GBP",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "ia-uuid-1", m["account_id"])
	assert.Equal(t, "CLEARING_GBP", m["account_code"])
	assert.Equal(t, "ACTIVE", m["status"])
}

func TestInternalAccountClient_InitiateAccount_MinimalParams(t *testing.T) {
	conn := newInternalAccountTestServer(t)
	client := NewInternalAccountClient(conn)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	result, err := client.InitiateAccount(ctx, map[string]any{
		"account_code": "MINIMAL",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "ia-uuid-1", m["account_id"])
	assert.Equal(t, "MINIMAL", m["account_code"])
}
