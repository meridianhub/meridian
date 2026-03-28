// Package service implements services for the party reference data domain
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/verification"
)

// VerificationService errors
var (
	ErrPartyNotFoundForVerification   = errors.New("party not found for verification")
	ErrVerificationProviderNil        = errors.New("verification provider cannot be nil")
	ErrVerificationRepositoryNil      = errors.New("verification repository cannot be nil")
	ErrVerificationAlreadyCompleted   = errors.New("verification already in terminal state")
	ErrInvalidVerificationStatusValue = errors.New("invalid verification status value")
)

// VerificationEventPublisher defines the interface for publishing verification events.
// Implementations may publish to Kafka, in-memory channels, etc.
type VerificationEventPublisher interface {
	// PublishVerificationCompleted publishes a PartyVerificationCompleted event
	PublishVerificationCompleted(ctx context.Context, event VerificationCompletedEvent) error
}

// VerificationCompletedEvent represents the data for a party verification completed event
type VerificationCompletedEvent struct {
	EventID        string
	PartyID        string
	VerificationID string
	Provider       string
	Status         string
	RiskScore      *float64
	Reason         *string
	CompletedAt    time.Time
	Metadata       map[string]string
}

// VerificationService handles KYC/AML verification operations
type VerificationService struct {
	partyRepo        PartyRepository
	verificationRepo VerificationRepository
	provider         verification.Provider
	eventPublisher   VerificationEventPublisher
	logger           *slog.Logger
}

// PartyRepository defines the interface for party lookup operations
type PartyRepository interface {
	FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
	ExistsByID(ctx context.Context, partyID uuid.UUID) (bool, error)
}

// VerificationRepository defines the interface for verification persistence operations
type VerificationRepository interface {
	CreateVerification(ctx context.Context, verification *persistence.PartyVerificationEntity) error
	UpdateVerificationStatus(ctx context.Context, verificationID uuid.UUID, status string, riskScore *float64, reason *string, completedAt *time.Time, currentVersion int64) error
	UpdateVerificationMetadata(ctx context.Context, verificationID uuid.UUID, metadata string) error
	GetVerificationByID(ctx context.Context, id uuid.UUID) (*persistence.PartyVerificationEntity, error)
	GetVerificationByProviderID(ctx context.Context, verificationID string) (*persistence.PartyVerificationEntity, error)
	ListVerificationsByParty(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyVerificationEntity, error)
}

// NewVerificationService creates a new verification service
func NewVerificationService(
	partyRepo PartyRepository,
	verificationRepo VerificationRepository,
	provider verification.Provider,
	eventPublisher VerificationEventPublisher,
	logger *slog.Logger,
) (*VerificationService, error) {
	if verificationRepo == nil {
		return nil, ErrVerificationRepositoryNil
	}
	if provider == nil {
		return nil, ErrVerificationProviderNil
	}

	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &VerificationService{
		partyRepo:        partyRepo,
		verificationRepo: verificationRepo,
		provider:         provider,
		eventPublisher:   eventPublisher,
		logger:           logger,
	}, nil
}

// InitiateVerificationRequest contains the parameters for initiating a verification
type InitiateVerificationRequest struct {
	PartyID  uuid.UUID
	Provider string // Provider name to use (if multiple configured)
}

// InitiateVerificationResponse contains the result of initiating a verification
type InitiateVerificationResponse struct {
	VerificationID         uuid.UUID // Internal verification ID
	ProviderVerificationID string    // Provider's external verification ID
	Status                 string
	CreatedAt              time.Time
}

// InitiateVerification starts an async verification for a party.
// Creates a record with PENDING status and returns immediately.
// The provider processes the verification asynchronously and calls back via webhook.
func (s *VerificationService) InitiateVerification(ctx context.Context, req InitiateVerificationRequest) (*InitiateVerificationResponse, error) {
	// Verify party exists
	exists, err := s.partyRepo.ExistsByID(ctx, req.PartyID)
	if err != nil {
		s.logger.Error("failed to check party existence",
			"party_id", req.PartyID,
			"error", err)
		return nil, err
	}
	if !exists {
		s.logger.Warn("party not found for verification",
			"party_id", req.PartyID)
		return nil, ErrPartyNotFoundForVerification
	}

	// For now, we generate a provider verification ID
	// In a real implementation, this would call the provider's API
	providerVerificationID := uuid.New().String()

	// Create verification record with PENDING status
	entity := &persistence.PartyVerificationEntity{
		PartyID:        req.PartyID,
		VerificationID: providerVerificationID,
		Provider:       req.Provider,
		Status:         string(verification.StatusPending),
	}

	if err := s.verificationRepo.CreateVerification(ctx, entity); err != nil {
		s.logger.Error("failed to create verification record",
			"party_id", req.PartyID,
			"error", err)
		return nil, err
	}

	s.logger.Info("verification initiated",
		"verification_id", entity.ID,
		"party_id", req.PartyID,
		"provider", req.Provider,
		"provider_verification_id", providerVerificationID)

	return &InitiateVerificationResponse{
		VerificationID:         entity.ID,
		ProviderVerificationID: providerVerificationID,
		Status:                 entity.Status,
		CreatedAt:              entity.CreatedAt,
	}, nil
}

// UpdateVerificationRequest contains the parameters for updating a verification
type UpdateVerificationRequest struct {
	ProviderVerificationID string
	Status                 string
	RiskScore              *float64
	Reason                 *string
	CompletedAt            *time.Time
	Metadata               map[string]string
}

// UpdateVerification updates the status of a verification (typically called by webhook handler).
// Emits PartyVerificationCompleted event when status transitions to a terminal state.
func (s *VerificationService) UpdateVerification(ctx context.Context, req UpdateVerificationRequest) error {
	vStatus := verification.Status(req.Status)
	if !vStatus.IsValid() {
		return ErrInvalidVerificationStatusValue
	}

	entity, err := s.loadAndValidateVerification(ctx, req.ProviderVerificationID)
	if err != nil {
		return err
	}

	metadataJSON := marshalVerificationMetadata(req.Metadata)

	completedAt := req.CompletedAt
	if isTerminalStatus(vStatus) && completedAt == nil {
		now := time.Now()
		completedAt = &now
	}

	err = s.verificationRepo.UpdateVerificationStatus(
		ctx, entity.ID, req.Status, req.RiskScore, req.Reason, completedAt, entity.Version,
	)
	if err != nil {
		s.logger.Error("failed to update verification status",
			"verification_id", entity.ID, "error", err)
		return err
	}

	s.logger.Info("verification status updated",
		"verification_id", entity.ID,
		"party_id", entity.PartyID,
		"old_status", entity.Status,
		"new_status", req.Status)

	if isTerminalStatus(vStatus) {
		s.publishVerificationCompleted(ctx, entity, req, *completedAt)
	}

	if metadataJSON != nil {
		if err := s.verificationRepo.UpdateVerificationMetadata(ctx, entity.ID, *metadataJSON); err != nil {
			s.logger.Error("failed to update verification metadata",
				"verification_id", entity.ID, "error", err)
		}
	}

	return nil
}

// loadAndValidateVerification finds a verification by provider ID and checks it is not already in a terminal state.
func (s *VerificationService) loadAndValidateVerification(ctx context.Context, providerVerificationID string) (*persistence.PartyVerificationEntity, error) {
	entity, err := s.verificationRepo.GetVerificationByProviderID(ctx, providerVerificationID)
	if err != nil {
		s.logger.Error("failed to find verification",
			"provider_verification_id", providerVerificationID, "error", err)
		return nil, err
	}

	currentStatus := verification.Status(entity.Status)
	if isTerminalStatus(currentStatus) {
		s.logger.Warn("verification already in terminal state",
			"verification_id", entity.ID, "current_status", entity.Status)
		return nil, ErrVerificationAlreadyCompleted
	}

	return entity, nil
}

// marshalVerificationMetadata marshals metadata to JSON string pointer. Returns nil if metadata is empty.
func marshalVerificationMetadata(metadata map[string]string) *string {
	if len(metadata) == 0 {
		return nil
	}
	jsonBytes, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	jsonStr := string(jsonBytes)
	return &jsonStr
}

// publishVerificationCompleted emits a verification completed event. Errors are logged but not propagated.
func (s *VerificationService) publishVerificationCompleted(ctx context.Context, entity *persistence.PartyVerificationEntity, req UpdateVerificationRequest, completedAt time.Time) {
	if s.eventPublisher == nil {
		return
	}

	event := VerificationCompletedEvent{
		EventID:        uuid.New().String(),
		PartyID:        entity.PartyID.String(),
		VerificationID: entity.VerificationID,
		Provider:       entity.Provider,
		Status:         req.Status,
		RiskScore:      req.RiskScore,
		Reason:         req.Reason,
		CompletedAt:    completedAt,
		Metadata:       req.Metadata,
	}

	if err := s.eventPublisher.PublishVerificationCompleted(ctx, event); err != nil {
		s.logger.Error("failed to publish verification completed event",
			"verification_id", entity.ID, "error", err)
	} else {
		s.logger.Info("verification completed event published",
			"verification_id", entity.ID, "event_id", event.EventID)
	}
}

// GetVerification retrieves a verification by internal ID
func (s *VerificationService) GetVerification(ctx context.Context, verificationID uuid.UUID) (*persistence.PartyVerificationEntity, error) {
	return s.verificationRepo.GetVerificationByID(ctx, verificationID)
}

// ListVerificationsForParty retrieves all verifications for a party
func (s *VerificationService) ListVerificationsForParty(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyVerificationEntity, error) {
	return s.verificationRepo.ListVerificationsByParty(ctx, partyID)
}

// isTerminalStatus checks if a verification status is terminal (no further transitions)
func isTerminalStatus(status verification.Status) bool {
	switch status {
	case verification.StatusPending:
		return false
	case verification.StatusApproved, verification.StatusRejected, verification.StatusManualReview:
		return true
	}
	return false
}
