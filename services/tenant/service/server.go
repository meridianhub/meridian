// Package service implements the TenantService gRPC server.
package service

import (
	"errors"
	"log/slog"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
)

// ErrUnknownStatus indicates an unspecified or unknown tenant status was provided.
var ErrUnknownStatus = errors.New("unspecified or unknown tenant status")

// Service implements the TenantService gRPC server.
type Service struct {
	pb.UnimplementedTenantServiceServer
	repo        *persistence.Repository
	provisioner provisioner.SchemaProvisioner
	partyClient PartyClient
	slugCache   *SlugCache
	logger      *slog.Logger
}

// NewService creates a new TenantService.
// The provisioner parameter is optional; if nil, schema provisioning is skipped during tenant creation.
// The partyClient parameter is optional; if nil, party registration is skipped during tenant creation.
// The slugCache parameter is optional; if nil, slug caching is disabled.
func NewService(repo *persistence.Repository, prov provisioner.SchemaProvisioner, partyClient PartyClient, slugCache *SlugCache, logger *slog.Logger) *Service {
	return &Service{
		repo:        repo,
		provisioner: prov,
		partyClient: partyClient,
		slugCache:   slugCache,
		logger:      logger,
	}
}

// provisioningHintFromStatus converts a tenant status to a provisioning hint string.
// Returns "pending" for any in-progress provisioning status (PROVISIONING_PENDING or PROVISIONING),
// "active" otherwise. This provides a simple binary decision point for clients.
func provisioningHintFromStatus(status domain.Status) string {
	switch status {
	case domain.StatusProvisioningPending, domain.StatusProvisioning:
		return "pending"
	case domain.StatusProvisioningFailed, domain.StatusActive, domain.StatusSuspended, domain.StatusDeprovisioned:
		return "active"
	}
	// Unreachable for valid statuses, but return "active" as safe default
	return "active"
}
