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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// Compile-time interface satisfaction check.
var _ InternalAccountService = (*InternalAccountClient)(nil)

// ─── Mock gRPC server ──────────────────────────────────────────────────────

type fakeInternalAccountServer struct {
	internalaccountv1.UnimplementedInternalAccountServiceServer
	initiateFn func(context.Context, *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error)
}

func (f *fakeInternalAccountServer) InitiateInternalAccount(ctx context.Context, req *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error) {
	if f.initiateFn != nil {
		return f.initiateFn(ctx, req)
	}
	return &internalaccountv1.InitiateInternalAccountResponse{
		AccountId: "ia-uuid-1",
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountCode:   req.AccountCode,
			AccountStatus: internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
		},
	}, nil
}

// ─── Test setup ────────────────────────────────────────────────────────────

func newInternalAccountTestServerWith(t *testing.T, fakeSrv *fakeInternalAccountServer) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	internalaccountv1.RegisterInternalAccountServiceServer(srv, fakeSrv)

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

func newInternalAccountTestServer(t *testing.T) *grpc.ClientConn {
	return newInternalAccountTestServerWith(t, &fakeInternalAccountServer{})
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
	assert.Contains(t, m["status"], "ACTIVE")
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

// ─── Idempotency tests ──────────────────────────────────────────────────────

func TestInternalAccountClient_InitiateAccount_AlreadyExists_TreatedAsSuccess(t *testing.T) {
	srv := &fakeInternalAccountServer{
		initiateFn: func(_ context.Context, _ *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "account code already exists: CLEARING_GBP")
		},
	}
	conn := newInternalAccountTestServerWith(t, srv)
	client := NewInternalAccountClient(conn)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	result, err := client.InitiateAccount(ctx, map[string]any{
		"account_code": "CLEARING_GBP",
	})
	require.NoError(t, err, "AlreadyExists should be treated as idempotent success")
	m := result.(map[string]any)
	assert.Equal(t, "CLEARING_GBP", m["account_code"])
	assert.Equal(t, "ACCOUNT_STATUS_ACTIVE", m["status"])
}

func TestInternalAccountClient_InitiateAccount_OtherError_Propagated(t *testing.T) {
	srv := &fakeInternalAccountServer{
		initiateFn: func(_ context.Context, _ *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error) {
			return nil, status.Error(codes.Internal, "database unavailable")
		},
	}
	conn := newInternalAccountTestServerWith(t, srv)
	client := NewInternalAccountClient(conn)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	_, err := client.InitiateAccount(ctx, map[string]any{
		"account_code": "WILL_FAIL",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")
}
