// Package service implements the TenantService gRPC server.
package service

import (
	"context"
	"fmt"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Compile-time interface assertion.
var _ PartyClient = (*PartyClientAdapter)(nil)

// PartyClientAdapter wraps the service-owned party client and implements the
// PartyClient interface expected by the tenant service.
//
// This adapter provides the translation layer between the standardized party/client
// package and the tenant-specific interface, including tenant-specific error handling.
type PartyClientAdapter struct {
	client  *partyclient.Client
	cleanup func()
}

// NewPartyClientAdapter creates a new adapter wrapping the service-owned party client.
// The cleanup function should be called when the adapter is no longer needed.
func NewPartyClientAdapter(client *partyclient.Client, cleanup func()) *PartyClientAdapter {
	return &PartyClientAdapter{
		client:  client,
		cleanup: cleanup,
	}
}

// RegisterParty creates a new party in the Party Reference Data Directory.
// Implements the PartyClient interface with tenant-specific error handling.
func (a *PartyClientAdapter) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error) {
	resp, err := a.client.RegisterParty(ctx, req)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() { //nolint:exhaustive // Only handling specific error codes
			case codes.AlreadyExists:
				return nil, fmt.Errorf("%w: party already exists", ErrPartyRegistrationFailed)
			case codes.InvalidArgument:
				return nil, fmt.Errorf("%w: %s", ErrPartyRegistrationFailed, st.Message())
			case codes.Unavailable:
				// Transient error - service temporarily unavailable, may be retried
				return nil, fmt.Errorf("%w: %v", ErrPartyServiceUnavailable, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
			case codes.DeadlineExceeded:
				// Transient error - request timed out, may be retried
				return nil, fmt.Errorf("%w: %v", ErrPartyServiceTimeout, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
			}
		}
		return nil, fmt.Errorf("%w: %v", ErrPartyRegistrationFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}

	return resp.Party, nil
}

// Close terminates the client connection gracefully.
func (a *PartyClientAdapter) Close() error {
	if a.cleanup != nil {
		a.cleanup()
	}
	return nil
}
