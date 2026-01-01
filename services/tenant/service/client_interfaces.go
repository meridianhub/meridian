// Package service implements the TenantService gRPC server.
package service

import (
	"context"
	"errors"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
)

// Party client errors.
var (
	// ErrPartyRegistrationFailed is returned when party registration fails.
	ErrPartyRegistrationFailed = errors.New("party registration failed")
	// ErrPartyServiceUnavailable is returned when the party service is temporarily unavailable.
	ErrPartyServiceUnavailable = errors.New("party service unavailable")
	// ErrPartyServiceTimeout is returned when the party service request times out.
	ErrPartyServiceTimeout = errors.New("party service timeout")
)

// PartyClient defines the interface for communicating with the Party service.
//
// The Tenant service uses this client to register a corresponding Party
// when a new tenant is created, establishing the link between platform
// infrastructure (Tenant) and BIAN domain entities (Party.Organization).
type PartyClient interface {
	// RegisterParty creates a new party in the Party Reference Data Directory.
	// Returns the registered party with its assigned party_id.
	RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error)

	// Close terminates the client connection gracefully.
	Close() error
}
