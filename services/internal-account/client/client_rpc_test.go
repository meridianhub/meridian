package client

import (
	"context"
	"net"
	"testing"
	"time"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fullMockServer implements all InternalAccountService methods for RPC testing.
type fullMockServer struct {
	internalaccountv1.UnimplementedInternalAccountServiceServer
}

func (m *fullMockServer) InitiateInternalAccount(_ context.Context, req *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error) {
	return &internalaccountv1.InitiateInternalAccountResponse{
		AccountId: "generated-id-001",
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountId:      "generated-id-001",
			AccountCode:    req.GetAccountCode(),
			Name:           req.GetName(),
			BehaviorClass:  req.GetProductTypeCode(),
			AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			InstrumentCode: req.GetInstrumentCode(),
			CreatedAt:      timestamppb.Now(),
			UpdatedAt:      timestamppb.Now(),
			Version:        1,
		},
	}, nil
}

func (m *fullMockServer) ControlInternalAccount(_ context.Context, req *internalaccountv1.ControlInternalAccountRequest) (*internalaccountv1.ControlInternalAccountResponse, error) {
	return &internalaccountv1.ControlInternalAccountResponse{
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountId:      req.GetAccountId(),
			AccountCode:    "TEST-CODE",
			Name:           "Test Account",
			AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED,
			InstrumentCode: "USD",
			CreatedAt:      timestamppb.Now(),
			UpdatedAt:      timestamppb.Now(),
			Version:        2,
		},
		ActionTimestamp: timestamppb.Now(),
	}, nil
}

func (m *fullMockServer) UpdateInternalAccount(_ context.Context, req *internalaccountv1.UpdateInternalAccountRequest) (*internalaccountv1.UpdateInternalAccountResponse, error) {
	return &internalaccountv1.UpdateInternalAccountResponse{
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountId:      req.GetAccountId(),
			AccountCode:    "TEST-CODE",
			Name:           "Updated Account",
			AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			InstrumentCode: "USD",
			CreatedAt:      timestamppb.Now(),
			UpdatedAt:      timestamppb.Now(),
			Version:        2,
		},
	}, nil
}

func (m *fullMockServer) RetrieveInternalAccount(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	return &internalaccountv1.RetrieveInternalAccountResponse{
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountId:      req.GetAccountId(),
			AccountCode:    "NOSTRO-USD-001",
			Name:           "USD Nostro",
			BehaviorClass:  "NOSTRO",
			AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			InstrumentCode: "USD",
			CreatedAt:      timestamppb.Now(),
			UpdatedAt:      timestamppb.Now(),
			Version:        1,
		},
	}, nil
}

func (m *fullMockServer) ListInternalAccounts(_ context.Context, _ *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error) {
	return &internalaccountv1.ListInternalAccountsResponse{
		Facilities: []*internalaccountv1.InternalAccountFacility{
			{
				AccountId:      "acct-001",
				AccountCode:    "CLR-GBP-001",
				Name:           "GBP Clearing",
				AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
				InstrumentCode: "GBP",
			},
			{
				AccountId:      "acct-002",
				AccountCode:    "NOSTRO-USD-001",
				Name:           "USD Nostro",
				AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
				InstrumentCode: "USD",
			},
		},
	}, nil
}

func (m *fullMockServer) GetBalance(_ context.Context, req *internalaccountv1.GetBalanceRequest) (*internalaccountv1.GetBalanceResponse, error) {
	return &internalaccountv1.GetBalanceResponse{
		AccountId: req.GetAccountId(),
		CurrentBalance: &quantityv1.InstrumentAmount{
			InstrumentCode: "USD",
			Amount:         "500.00",
			Version:        1,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func setupBufconnClient(t *testing.T) *Client {
	t.Helper()

	mock := &fullMockServer{}

	listener := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	internalaccountv1.RegisterInternalAccountServiceServer(srv, mock)

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(listener)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	c := &Client{
		conn:            conn,
		internalAccount: internalaccountv1.NewInternalAccountServiceClient(conn),
		timeout:         5 * time.Second,
	}

	t.Cleanup(func() {
		conn.Close()
		srv.GracefulStop()
		<-serveDone
		listener.Close()
	})

	return c
}

func TestControlInternalAccount_Success(t *testing.T) {
	c := setupBufconnClient(t)

	resp, err := c.ControlInternalAccount(context.Background(), &internalaccountv1.ControlInternalAccountRequest{
		AccountId:     "IBA-001",
		ControlAction: internalaccountv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Maintenance period for testing",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetFacility().GetAccountId())
	assert.Equal(t, internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED, resp.GetFacility().GetAccountStatus())
	assert.NotNil(t, resp.GetActionTimestamp())
}

func TestUpdateInternalAccount_Success(t *testing.T) {
	c := setupBufconnClient(t)

	resp, err := c.UpdateInternalAccount(context.Background(), &internalaccountv1.UpdateInternalAccountRequest{
		AccountId: "IBA-001",
		Name:      "Updated Account",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetFacility().GetAccountId())
	assert.Equal(t, "Updated Account", resp.GetFacility().GetName())
}

func TestListInternalAccounts_Success(t *testing.T) {
	c := setupBufconnClient(t)

	resp, err := c.ListInternalAccounts(context.Background(), &internalaccountv1.ListInternalAccountsRequest{})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.GetFacilities(), 2)
	assert.Equal(t, "acct-001", resp.GetFacilities()[0].GetAccountId())
	assert.Equal(t, "acct-002", resp.GetFacilities()[1].GetAccountId())
}

func TestInitiateInternalAccount_SuccessPath(t *testing.T) {
	c := setupBufconnClient(t)

	resp, err := c.InitiateInternalAccount(context.Background(), &internalaccountv1.InitiateInternalAccountRequest{
		AccountCode:     "NOSTRO-USD-001",
		Name:            "USD Nostro",
		ProductTypeCode: "NOSTRO_USD",
		InstrumentCode:  "USD",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "generated-id-001", resp.GetAccountId())
	assert.Equal(t, "NOSTRO-USD-001", resp.GetFacility().GetAccountCode())
}

func TestRetrieveInternalAccount_SuccessPath(t *testing.T) {
	c := setupBufconnClient(t)

	resp, err := c.RetrieveInternalAccount(context.Background(), &internalaccountv1.RetrieveInternalAccountRequest{
		AccountId: "IBA-001",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetFacility().GetAccountId())
	assert.Equal(t, "NOSTRO-USD-001", resp.GetFacility().GetAccountCode())
}

func TestGetBalance_SuccessPath(t *testing.T) {
	c := setupBufconnClient(t)

	resp, err := c.GetBalance(context.Background(), &internalaccountv1.GetBalanceRequest{
		AccountId: "IBA-001",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetAccountId())
	assert.Equal(t, "500.00", resp.GetCurrentBalance().GetAmount())
	assert.Equal(t, "USD", resp.GetCurrentBalance().GetInstrumentCode())
}

func setupBufconnClientWithResilience(t *testing.T) *Client {
	t.Helper()

	c := setupBufconnClient(t)
	resilienceConfig := clients.DefaultResilientClientConfig("test-internal-account")
	c.resilient = clients.NewResilientClient(resilienceConfig)

	return c
}

func TestControlInternalAccount_WithResilience(t *testing.T) {
	c := setupBufconnClientWithResilience(t)

	resp, err := c.ControlInternalAccount(context.Background(), &internalaccountv1.ControlInternalAccountRequest{
		AccountId:     "IBA-001",
		ControlAction: internalaccountv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Maintenance period for testing",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetFacility().GetAccountId())
}

func TestUpdateInternalAccount_WithResilience(t *testing.T) {
	c := setupBufconnClientWithResilience(t)

	resp, err := c.UpdateInternalAccount(context.Background(), &internalaccountv1.UpdateInternalAccountRequest{
		AccountId: "IBA-001",
		Name:      "Updated Account",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetFacility().GetAccountId())
}

func TestListInternalAccounts_WithResilience(t *testing.T) {
	c := setupBufconnClientWithResilience(t)

	resp, err := c.ListInternalAccounts(context.Background(), &internalaccountv1.ListInternalAccountsRequest{})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.GetFacilities(), 2)
}

func TestInitiateInternalAccount_WithResilience(t *testing.T) {
	c := setupBufconnClientWithResilience(t)

	resp, err := c.InitiateInternalAccount(context.Background(), &internalaccountv1.InitiateInternalAccountRequest{
		AccountCode:     "NOSTRO-USD-001",
		Name:            "USD Nostro",
		ProductTypeCode: "NOSTRO_USD",
		InstrumentCode:  "USD",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "generated-id-001", resp.GetAccountId())
}

func TestRetrieveInternalAccount_WithResilience(t *testing.T) {
	c := setupBufconnClientWithResilience(t)

	resp, err := c.RetrieveInternalAccount(context.Background(), &internalaccountv1.RetrieveInternalAccountRequest{
		AccountId: "IBA-001",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetFacility().GetAccountId())
}

func TestGetBalance_WithResilience(t *testing.T) {
	c := setupBufconnClientWithResilience(t)

	resp, err := c.GetBalance(context.Background(), &internalaccountv1.GetBalanceRequest{
		AccountId: "IBA-001",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "IBA-001", resp.GetAccountId())
}

func TestClose_WithActiveConnection(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	go func() { _ = srv.Serve(listener) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	c := &Client{
		conn:            conn,
		internalAccount: internalaccountv1.NewInternalAccountServiceClient(conn),
		timeout:         5 * time.Second,
	}

	err = c.Close()
	assert.NoError(t, err)

	t.Cleanup(func() {
		srv.GracefulStop()
		listener.Close()
	})
}
