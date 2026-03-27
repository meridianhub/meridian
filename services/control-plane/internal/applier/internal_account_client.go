package applier

import (
	"context"
	"fmt"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InternalAccountClient wraps the internal-account gRPC client to implement
// InternalAccountService for use as a saga handler dependency.
//
// The client translates between the flat map[string]any parameter convention used
// by saga handlers and the typed proto messages required by the gRPC service.
type InternalAccountClient struct {
	client internalaccountv1.InternalAccountServiceClient
}

// NewInternalAccountClient creates a new InternalAccountClient from a gRPC connection.
func NewInternalAccountClient(conn *grpc.ClientConn) *InternalAccountClient {
	return &InternalAccountClient{
		client: internalaccountv1.NewInternalAccountServiceClient(conn),
	}
}

// InitiateAccount implements InternalAccountService.
// Converts Starlark params to an InitiateInternalAccountRequest and calls the gRPC service.
func (c *InternalAccountClient) InitiateAccount(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &internalaccountv1.InitiateInternalAccountRequest{}
	req.AccountCode, _ = params["account_code"].(string)
	req.Name, _ = params["name"].(string)
	req.InstrumentCode, _ = params["instrument_code"].(string)
	req.Description, _ = params["description"].(string)
	req.ProductTypeCode, _ = params["account_type"].(string)
	req.OrgPartyId, _ = params["owner_organization"].(string)

	callCtx := prepareCallContext(ctx)
	resp, err := c.client.InitiateInternalAccount(callCtx, req)
	if err != nil {
		// Idempotency: treat AlreadyExists as success for manifest re-apply scenarios
		// where the account was already created by a previous apply.
		// Look up the existing account to return account_id (required by the saga script).
		if status.Code(err) == codes.AlreadyExists {
			return c.handleAlreadyExists(callCtx, req.AccountCode)
		}
		return nil, fmt.Errorf("initiate internal account: %w", err)
	}

	return map[string]any{
		"account_id":   resp.GetAccountId(),
		"account_code": req.AccountCode,
		"status":       resp.GetFacility().GetAccountStatus().String(),
	}, nil
}

// handleAlreadyExists resolves the existing account by code so that
// downstream saga steps receive account_id. The RetrieveInternalAccount
// RPC accepts account_code as well as UUID.
func (c *InternalAccountClient) handleAlreadyExists(ctx context.Context, accountCode string) (any, error) {
	resp, err := c.client.RetrieveInternalAccount(ctx, &internalaccountv1.RetrieveInternalAccountRequest{
		AccountId: accountCode,
	})
	if err != nil {
		return nil, fmt.Errorf("initiate internal account: account already exists but lookup failed: %w", err)
	}

	facility := resp.GetFacility()
	return map[string]any{
		"account_id":   facility.GetAccountId(),
		"account_code": facility.GetAccountCode(),
		"status":       facility.GetAccountStatus().String(),
	}, nil
}

// Ensure InternalAccountClient implements InternalAccountService at compile time.
var _ InternalAccountService = (*InternalAccountClient)(nil)
