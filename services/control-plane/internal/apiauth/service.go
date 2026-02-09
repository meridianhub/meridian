// Package apiauth implements the AuthService gRPC server for API key validation.
// The Gateway calls ValidateAPIKey to verify keys stored in tenant schemas.
package apiauth

import (
	"context"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/staff"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// Service implements the AuthService gRPC server.
type Service struct {
	controlplanev1.UnimplementedAuthServiceServer

	staffSvc *staff.Service
	logger   *slog.Logger
}

// NewService creates a new AuthService.
func NewService(gormDB *gorm.DB, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		staffSvc: staff.NewService(gormDB, logger),
		logger:   logger,
	}
}

// ValidateAPIKey verifies an API key by querying the tenant schema.
// The tenant ID must be present in the context (set by the gateway before calling).
func (s *Service) ValidateAPIKey(ctx context.Context, req *controlplanev1.ValidateAPIKeyRequest) (*controlplanev1.ValidateAPIKeyResponse, error) {
	if req.GetKeyPrefix() == "" || req.GetPlaintextKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "key_prefix and plaintext_key are required")
	}

	// Tenant context must be set by the caller (gateway resolves slug -> tenant_id)
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "tenant context required")
	}

	result, err := s.staffSvc.ValidateAPIKeyFull(ctx, req.GetKeyPrefix(), req.GetPlaintextKey())
	if err != nil {
		s.logger.WarnContext(ctx, "API key validation failed",
			"key_prefix", req.GetKeyPrefix(),
			"error", err)
		return &controlplanev1.ValidateAPIKeyResponse{Valid: false}, nil
	}

	identity := result.User.Email
	if result.User.Name != "" {
		identity = result.User.Name
	}

	return &controlplanev1.ValidateAPIKeyResponse{
		Valid:        true,
		TenantId:     tenantID.String(),
		Identity:     identity,
		Scopes:       result.Scopes,
		RateLimitRps: int32(result.RateLimitRPS),
	}, nil
}
