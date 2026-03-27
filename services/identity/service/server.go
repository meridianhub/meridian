// Package service implements gRPC handlers for the identity and access management domain.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Service errors
var (
	ErrRepositoryNil = errors.New("repository cannot be nil")
)

// OutboxWriter queues an email for delivery. A narrow interface so the service
// does not depend on the full email.OutboxRepository.
type OutboxWriter interface {
	Enqueue(ctx context.Context, entry *email.OutboxEntry) error
}

// Option configures a Service.
type Option func(*Service)

// WithEmailOutbox sets the outbox writer used to queue notification emails.
// If not provided (or nil), email notifications are silently skipped.
func WithEmailOutbox(outbox OutboxWriter) Option {
	return func(s *Service) {
		s.emailOutbox = outbox
	}
}

// WithBaseURL sets the base URL used to construct invitation accept links.
// Defaults to "https://app.meridianhub.cloud" if not provided.
func WithBaseURL(baseURL string) Option {
	return func(s *Service) {
		s.baseURL = baseURL
	}
}

// Service implements the IdentityService gRPC service.
type Service struct {
	pb.UnimplementedIdentityServiceServer
	repo        domain.Repository
	logger      *slog.Logger
	emailOutbox OutboxWriter
	baseURL     string
}

// NewService creates a new identity service with the required repository dependency.
func NewService(repo domain.Repository, logger *slog.Logger, opts ...Option) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	svc := &Service{
		repo:    repo,
		logger:  logger,
		baseURL: "https://app.meridianhub.cloud",
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

// --- Health Check ---

// Check implements grpc_health_v1.HealthServer.
func (s *Service) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

// Watch implements grpc_health_v1.HealthServer (streaming, not supported).
func (s *Service) Watch(_ *grpc_health_v1.HealthCheckRequest, _ grpc_health_v1.Health_WatchServer) error {
	return status.Errorf(codes.Unimplemented, "watch is not supported")
}

// --- Helpers ---

// getCallerHighestRole extracts the caller's highest role from context.
// Returns empty string if no roles are found.
func (s *Service) getCallerHighestRole(ctx context.Context) string {
	roles, ok := auth.GetRolesFromContext(ctx)
	if !ok || len(roles) == 0 {
		return ""
	}
	// Return the last role, which by convention is the highest in the list.
	// The auth interceptor orders roles by privilege level.
	highest := roles[0]
	for _, r := range roles[1:] {
		if domain.CanGrant(r, highest) {
			highest = r
		}
	}
	return highest
}

// verifyCallerOutranksTarget checks that the caller's role strictly outranks
// the target identity's highest active role. This prevents, for example, an
// Admin from suspending a Tenant Owner.
func (s *Service) verifyCallerOutranksTarget(ctx context.Context, targetID uuid.UUID, callerRole string) error {
	assignments, err := s.repo.FindRoleAssignments(ctx, targetID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to fetch target role assignments",
			"target_id", targetID,
			"error", err)
		return status.Errorf(codes.Internal, "failed to verify target permissions")
	}

	// Find highest active role of the target identity.
	targetHighest := ""
	for _, a := range assignments {
		if a.IsActive() {
			role := string(a.Role())
			if targetHighest == "" || domain.CanGrant(role, targetHighest) {
				targetHighest = role
			}
		}
	}

	// If the target has no active roles, caller with Admin+ can proceed.
	if targetHighest == "" {
		return nil
	}

	if !domain.CanGrant(callerRole, targetHighest) {
		return status.Errorf(codes.PermissionDenied, "insufficient privilege to act on identity with role %s", targetHighest)
	}
	return nil
}

// mapDomainError maps domain-layer errors to gRPC status errors.
func mapDomainError(err error, entity string) error {
	switch {
	case errors.Is(err, domain.ErrIdentityNotFound):
		return status.Errorf(codes.NotFound, "%s not found", entity)
	case errors.Is(err, domain.ErrEmailAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "email already exists")
	case errors.Is(err, domain.ErrAccountLocked):
		return status.Errorf(codes.FailedPrecondition, "account is locked")
	case errors.Is(err, domain.ErrInvalidStatusTransition):
		return status.Errorf(codes.FailedPrecondition, "invalid status transition")
	case errors.Is(err, domain.ErrInvitationNotFound):
		return status.Errorf(codes.NotFound, "invitation not found")
	case errors.Is(err, domain.ErrInvitationExpired):
		return status.Errorf(codes.FailedPrecondition, "invitation has expired")
	case errors.Is(err, domain.ErrInvitationAlreadyAccepted):
		return status.Errorf(codes.FailedPrecondition, "invitation has already been accepted")
	case errors.Is(err, domain.ErrVersionConflict):
		return status.Errorf(codes.Aborted, "version conflict: resource was modified by another transaction")
	default:
		return status.Errorf(codes.Internal, "internal error")
	}
}

// queueInvitationEmail enqueues an invitation email for the given invitee.
// Best-effort: errors are logged but not surfaced to the caller.
// The inviter's email is looked up by inviterID; if the lookup fails, "unknown" is used.
func (s *Service) queueInvitationEmail(ctx context.Context, invitee *domain.Identity, inviterID uuid.UUID, plaintextToken string, tenantID tenant.TenantID) {
	if s.emailOutbox == nil {
		return
	}

	inviterEmail := "unknown"
	if inviter, err := s.repo.FindByID(ctx, inviterID); err == nil {
		inviterEmail = inviter.Email()
	}

	tenantSlug := string(tenantID)
	if slug, ok := tenant.SlugFromContext(ctx); ok {
		tenantSlug = slug
	}

	acceptLink := fmt.Sprintf("%s/auth/accept-invitation?token=%s", s.baseURL, plaintextToken)

	entry := &email.OutboxEntry{
		IdempotencyKey: "invite-user:" + invitee.ID().String(),
		ToAddresses:    []string{invitee.Email()},
		FromAddress:    "noreply@meridianhub.cloud",
		Subject:        "You've been invited",
		TemplateName:   "invite-user",
		TemplateData: map[string]any{
			"TenantName":   string(tenantID),
			"TenantSlug":   tenantSlug,
			"InviterEmail": inviterEmail,
			"AcceptLink":   acceptLink,
			"SupportEmail": "support@meridianhub.cloud",
		},
	}

	if err := s.emailOutbox.Enqueue(ctx, entry); err != nil {
		if errors.Is(err, email.ErrDuplicateIdempotency) {
			s.logger.InfoContext(ctx, "invitation email already queued (idempotent)",
				"identity_id", invitee.ID())
			return
		}
		s.logger.ErrorContext(ctx, "failed to queue invitation email",
			"identity_id", invitee.ID(),
			"error", err)
	}
}
