package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test errors
var errKafkaUnavailable = errors.New("kafka unavailable")

// mockPartyRepository implements PartyRepository for testing
type mockPartyRepository struct {
	existsByIDFn func(ctx context.Context, partyID uuid.UUID) (bool, error)
	findByIDFn   func(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
}

func (m *mockPartyRepository) ExistsByID(ctx context.Context, partyID uuid.UUID) (bool, error) {
	if m.existsByIDFn != nil {
		return m.existsByIDFn(ctx, partyID)
	}
	return true, nil
}

func (m *mockPartyRepository) FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(ctx, partyID)
	}
	return nil, nil
}

// mockVerificationRepository implements VerificationRepository for testing
type mockVerificationRepository struct {
	verifications         map[uuid.UUID]*persistence.PartyVerificationEntity
	verificationsByProvID map[string]*persistence.PartyVerificationEntity
	createFn              func(ctx context.Context, v *persistence.PartyVerificationEntity) error
	updateStatusFn        func(ctx context.Context, id uuid.UUID, status string, riskScore *float64, reason *string, completedAt *time.Time, version int64) error
	updateMetadataFn      func(ctx context.Context, id uuid.UUID, metadata string) error
	getByIDFn             func(ctx context.Context, id uuid.UUID) (*persistence.PartyVerificationEntity, error)
	getByProviderIDFn     func(ctx context.Context, verificationID string) (*persistence.PartyVerificationEntity, error)
	listByPartyFn         func(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyVerificationEntity, error)
}

func newMockVerificationRepository() *mockVerificationRepository {
	return &mockVerificationRepository{
		verifications:         make(map[uuid.UUID]*persistence.PartyVerificationEntity),
		verificationsByProvID: make(map[string]*persistence.PartyVerificationEntity),
	}
}

func (m *mockVerificationRepository) CreateVerification(ctx context.Context, v *persistence.PartyVerificationEntity) error {
	if m.createFn != nil {
		return m.createFn(ctx, v)
	}
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	v.CreatedAt = time.Now()
	v.UpdatedAt = time.Now()
	v.Version = 1
	m.verifications[v.ID] = v
	m.verificationsByProvID[v.VerificationID] = v
	return nil
}

func (m *mockVerificationRepository) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, status string, riskScore *float64, reason *string, completedAt *time.Time, version int64) error {
	if m.updateStatusFn != nil {
		return m.updateStatusFn(ctx, id, status, riskScore, reason, completedAt, version)
	}
	v, ok := m.verifications[id]
	if !ok {
		return persistence.ErrVerificationNotFound
	}
	if v.Version != version {
		return persistence.ErrVersionConflict
	}
	v.Status = status
	if riskScore != nil {
		v.RiskScore = riskScore
	}
	if reason != nil {
		v.Reason = reason
	}
	if completedAt != nil {
		v.CompletedAt = completedAt
	}
	v.Version++
	v.UpdatedAt = time.Now()
	return nil
}

func (m *mockVerificationRepository) GetVerificationByID(ctx context.Context, id uuid.UUID) (*persistence.PartyVerificationEntity, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	v, ok := m.verifications[id]
	if !ok {
		return nil, persistence.ErrVerificationNotFound
	}
	return v, nil
}

func (m *mockVerificationRepository) GetVerificationByProviderID(ctx context.Context, verificationID string) (*persistence.PartyVerificationEntity, error) {
	if m.getByProviderIDFn != nil {
		return m.getByProviderIDFn(ctx, verificationID)
	}
	v, ok := m.verificationsByProvID[verificationID]
	if !ok {
		return nil, persistence.ErrVerificationNotFound
	}
	return v, nil
}

func (m *mockVerificationRepository) ListVerificationsByParty(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyVerificationEntity, error) {
	if m.listByPartyFn != nil {
		return m.listByPartyFn(ctx, partyID)
	}
	var result []persistence.PartyVerificationEntity
	for _, v := range m.verifications {
		if v.PartyID == partyID {
			result = append(result, *v)
		}
	}
	return result, nil
}

func (m *mockVerificationRepository) UpdateVerificationMetadata(ctx context.Context, id uuid.UUID, metadata string) error {
	if m.updateMetadataFn != nil {
		return m.updateMetadataFn(ctx, id, metadata)
	}
	v, ok := m.verifications[id]
	if !ok {
		return persistence.ErrVerificationNotFound
	}
	v.Metadata = &metadata
	v.UpdatedAt = time.Now()
	return nil
}

// mockEventPublisher implements VerificationEventPublisher for testing
type mockEventPublisher struct {
	publishedEvents []VerificationCompletedEvent
	publishFn       func(ctx context.Context, event VerificationCompletedEvent) error
}

func (m *mockEventPublisher) PublishVerificationCompleted(ctx context.Context, event VerificationCompletedEvent) error {
	if m.publishFn != nil {
		return m.publishFn(ctx, event)
	}
	m.publishedEvents = append(m.publishedEvents, event)
	return nil
}

// mockProvider implements verification.Provider for testing
type mockProvider struct{}

func (m *mockProvider) VerifyIdentity(_ context.Context, _ *domain.Party) (verification.Result, error) {
	return verification.Result{
		VerificationID: uuid.New().String(),
		Status:         verification.StatusPending,
	}, nil
}

func (m *mockProvider) CheckSanctions(_ context.Context, _ *domain.Party) (verification.SanctionsResult, error) {
	return verification.SanctionsResult{
		Status: verification.SanctionsStatusClear,
	}, nil
}

func (m *mockProvider) GetVerificationStatus(_ context.Context, verificationID string) (verification.Result, error) {
	return verification.Result{
		VerificationID: verificationID,
		Status:         verification.StatusPending,
	}, nil
}

func TestNewVerificationService(t *testing.T) {
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestNewVerificationService_NilRepo(t *testing.T) {
	partyRepo := &mockPartyRepository{}
	provider := &mockProvider{}

	_, err := NewVerificationService(partyRepo, nil, provider, nil, nil)
	assert.ErrorIs(t, err, ErrVerificationRepositoryNil)
}

func TestNewVerificationService_NilProvider(t *testing.T) {
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()

	_, err := NewVerificationService(partyRepo, verificationRepo, nil, nil, nil)
	assert.ErrorIs(t, err, ErrVerificationProviderNil)
}

func TestInitiateVerification_Success(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{
		existsByIDFn: func(_ context.Context, id uuid.UUID) (bool, error) {
			return id == partyID, nil
		},
	}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()
	resp, err := svc.InitiateVerification(ctx, InitiateVerificationRequest{
		PartyID:  partyID,
		Provider: "onfido",
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, resp.VerificationID)
	assert.NotEmpty(t, resp.ProviderVerificationID)
	assert.Equal(t, "PENDING", resp.Status)
	assert.False(t, resp.CreatedAt.IsZero())

	// Verify record was created
	created, err := verificationRepo.GetVerificationByID(ctx, resp.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, partyID, created.PartyID)
	assert.Equal(t, "onfido", created.Provider)
	assert.Equal(t, "PENDING", created.Status)
}

func TestInitiateVerification_PartyNotFound(t *testing.T) {
	partyRepo := &mockPartyRepository{
		existsByIDFn: func(_ context.Context, _ uuid.UUID) (bool, error) {
			return false, nil
		},
	}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = svc.InitiateVerification(ctx, InitiateVerificationRequest{
		PartyID:  uuid.New(),
		Provider: "onfido",
	})

	assert.ErrorIs(t, err, ErrPartyNotFoundForVerification)
}

func TestUpdateVerification_ToApproved(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	eventPublisher := &mockEventPublisher{}
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, eventPublisher, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a pending verification
	providerVerificationID := "prov-123"
	entity := &persistence.PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: providerVerificationID,
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Update to APPROVED
	riskScore := 0.15
	reason := "All checks passed"
	err = svc.UpdateVerification(ctx, UpdateVerificationRequest{
		ProviderVerificationID: providerVerificationID,
		Status:                 "APPROVED",
		RiskScore:              &riskScore,
		Reason:                 &reason,
	})
	require.NoError(t, err)

	// Verify status updated
	updated, err := verificationRepo.GetVerificationByProviderID(ctx, providerVerificationID)
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", updated.Status)
	assert.NotNil(t, updated.RiskScore)
	assert.InDelta(t, 0.15, *updated.RiskScore, 0.0001)
	assert.NotNil(t, updated.CompletedAt)

	// Verify event was published
	require.Len(t, eventPublisher.publishedEvents, 1)
	event := eventPublisher.publishedEvents[0]
	assert.Equal(t, partyID.String(), event.PartyID)
	assert.Equal(t, "APPROVED", event.Status)
	assert.InDelta(t, 0.15, *event.RiskScore, 0.0001)
}

func TestUpdateVerification_ToRejected(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	eventPublisher := &mockEventPublisher{}
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, eventPublisher, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a pending verification
	providerVerificationID := "prov-456"
	entity := &persistence.PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: providerVerificationID,
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Update to REJECTED
	reason := "Document fraud detected"
	err = svc.UpdateVerification(ctx, UpdateVerificationRequest{
		ProviderVerificationID: providerVerificationID,
		Status:                 "REJECTED",
		Reason:                 &reason,
	})
	require.NoError(t, err)

	// Verify event was published with REJECTED status
	require.Len(t, eventPublisher.publishedEvents, 1)
	event := eventPublisher.publishedEvents[0]
	assert.Equal(t, "REJECTED", event.Status)
	assert.Equal(t, "Document fraud detected", *event.Reason)
}

func TestUpdateVerification_AlreadyCompleted(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create an already approved verification
	providerVerificationID := "prov-789"
	entity := &persistence.PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: providerVerificationID,
		Provider:       "onfido",
		Status:         "APPROVED", // Already terminal
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Try to update - should fail
	err = svc.UpdateVerification(ctx, UpdateVerificationRequest{
		ProviderVerificationID: providerVerificationID,
		Status:                 "REJECTED",
	})
	assert.ErrorIs(t, err, ErrVerificationAlreadyCompleted)
}

func TestUpdateVerification_InvalidStatus(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a pending verification
	providerVerificationID := "prov-invalid"
	entity := &persistence.PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: providerVerificationID,
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Try to update with invalid status
	err = svc.UpdateVerification(ctx, UpdateVerificationRequest{
		ProviderVerificationID: providerVerificationID,
		Status:                 "INVALID_STATUS",
	})
	assert.ErrorIs(t, err, ErrInvalidVerificationStatusValue)
}

func TestUpdateVerification_ToManualReview(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	eventPublisher := &mockEventPublisher{}
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, eventPublisher, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a pending verification
	providerVerificationID := "prov-manual"
	entity := &persistence.PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: providerVerificationID,
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Update to MANUAL_REVIEW
	reason := "Inconclusive document check"
	err = svc.UpdateVerification(ctx, UpdateVerificationRequest{
		ProviderVerificationID: providerVerificationID,
		Status:                 "MANUAL_REVIEW",
		Reason:                 &reason,
	})
	require.NoError(t, err)

	// MANUAL_REVIEW is terminal, so event should be published
	require.Len(t, eventPublisher.publishedEvents, 1)
	event := eventPublisher.publishedEvents[0]
	assert.Equal(t, "MANUAL_REVIEW", event.Status)
}

func TestUpdateVerification_EventPublisherError(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	eventPublisher := &mockEventPublisher{
		publishFn: func(_ context.Context, _ VerificationCompletedEvent) error {
			return errKafkaUnavailable
		},
	}
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, eventPublisher, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a pending verification
	providerVerificationID := "prov-event-err"
	entity := &persistence.PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: providerVerificationID,
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Update to APPROVED - should succeed even if event fails
	err = svc.UpdateVerification(ctx, UpdateVerificationRequest{
		ProviderVerificationID: providerVerificationID,
		Status:                 "APPROVED",
	})
	require.NoError(t, err) // Status update succeeds

	// Verify status was updated despite event failure
	updated, err := verificationRepo.GetVerificationByProviderID(ctx, providerVerificationID)
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", updated.Status)
}

func TestListVerificationsForParty(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create multiple verifications
	for i := 1; i <= 3; i++ {
		entity := &persistence.PartyVerificationEntity{
			PartyID:        partyID,
			VerificationID: uuid.New().String(),
			Provider:       "onfido",
			Status:         "PENDING",
		}
		err = verificationRepo.CreateVerification(ctx, entity)
		require.NoError(t, err)
	}

	// List verifications
	verifications, err := svc.ListVerificationsForParty(ctx, partyID)
	require.NoError(t, err)
	assert.Len(t, verifications, 3)
}

func TestUpdateVerification_WithMetadata(t *testing.T) {
	partyID := uuid.New()
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	eventPublisher := &mockEventPublisher{}
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, eventPublisher, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a pending verification
	providerVerificationID := "prov-metadata"
	entity := &persistence.PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: providerVerificationID,
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Update with metadata
	riskScore := 0.1
	err = svc.UpdateVerification(ctx, UpdateVerificationRequest{
		ProviderVerificationID: providerVerificationID,
		Status:                 "APPROVED",
		RiskScore:              &riskScore,
		Metadata: map[string]string{
			"document_type": "passport",
			"confidence":    "0.95",
		},
	})
	require.NoError(t, err)

	// Verify metadata was persisted
	updated, err := verificationRepo.GetVerificationByProviderID(ctx, providerVerificationID)
	require.NoError(t, err)
	require.NotNil(t, updated.Metadata, "metadata should be persisted")

	// Verify metadata content is valid JSON with expected keys
	var metadata map[string]string
	err = json.Unmarshal([]byte(*updated.Metadata), &metadata)
	require.NoError(t, err)
	assert.Equal(t, "passport", metadata["document_type"])
	assert.Equal(t, "0.95", metadata["confidence"])

	// Verify event was still published
	require.Len(t, eventPublisher.publishedEvents, 1)
	assert.Equal(t, "APPROVED", eventPublisher.publishedEvents[0].Status)
}

func TestGetVerification_Success(t *testing.T) {
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a verification
	entity := &persistence.PartyVerificationEntity{
		PartyID:        uuid.New(),
		VerificationID: "prov-get-test",
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err = verificationRepo.CreateVerification(ctx, entity)
	require.NoError(t, err)

	// Retrieve it
	result, err := svc.GetVerification(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.ID, result.ID)
	assert.Equal(t, "prov-get-test", result.VerificationID)
}

func TestGetVerification_NotFound(t *testing.T) {
	partyRepo := &mockPartyRepository{}
	verificationRepo := newMockVerificationRepository()
	provider := &mockProvider{}

	svc, err := NewVerificationService(partyRepo, verificationRepo, provider, nil, nil)
	require.NoError(t, err)

	_, err = svc.GetVerification(context.Background(), uuid.New())
	assert.ErrorIs(t, err, persistence.ErrVerificationNotFound)
}

func TestIsTerminalStatus(t *testing.T) {
	tests := []struct {
		status   verification.Status
		terminal bool
	}{
		{verification.StatusPending, false},
		{verification.StatusApproved, true},
		{verification.StatusRejected, true},
		{verification.StatusManualReview, true},
		{verification.Status("UNKNOWN"), false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			assert.Equal(t, tc.terminal, isTerminalStatus(tc.status))
		})
	}
}
