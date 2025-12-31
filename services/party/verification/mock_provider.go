package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/party/domain"
)

// MockProvider is a test implementation of Provider.
// It provides configurable behavior for testing verification workflows.
type MockProvider struct {
	// AlwaysApprove determines whether verifications automatically approve (true)
	// or reject (false)
	AlwaysApprove bool

	// SimulatedDelay adds an artificial delay to simulate async provider behavior.
	// When set, the provider will wait for this duration before returning results.
	// Uses context cancellation for clean shutdown - no time.Sleep.
	SimulatedDelay time.Duration

	// AsyncMode when true returns PENDING status initially; subsequent calls to
	// GetVerificationStatus return the final status after SimulatedDelay has passed.
	AsyncMode bool

	// mu protects the verifications map
	mu sync.RWMutex

	// verifications stores verification results keyed by verification ID
	verifications map[string]verificationRecord
}

// verificationRecord tracks a verification's state and timing for async simulation
type verificationRecord struct {
	result    Result
	createdAt time.Time
}

// NewMockProvider creates a new MockProvider with default settings.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		AlwaysApprove:  true,
		SimulatedDelay: 0,
		AsyncMode:      false,
		verifications:  make(map[string]verificationRecord),
	}
}

// WithAlwaysApprove sets whether verifications should always approve.
func (m *MockProvider) WithAlwaysApprove(approve bool) *MockProvider {
	m.AlwaysApprove = approve
	return m
}

// WithSimulatedDelay sets the simulated processing delay.
func (m *MockProvider) WithSimulatedDelay(delay time.Duration) *MockProvider {
	m.SimulatedDelay = delay
	return m
}

// WithAsyncMode enables async simulation mode.
func (m *MockProvider) WithAsyncMode(async bool) *MockProvider {
	m.AsyncMode = async
	return m
}

// VerifyIdentity implements Provider.VerifyIdentity.
// Generates a deterministic verification ID based on the party's ID and current time.
func (m *MockProvider) VerifyIdentity(ctx context.Context, party *domain.Party) (Result, error) {
	// Generate deterministic verification ID from party ID
	verificationID := m.generateVerificationID(party.ID().String())

	// Handle simulated delay if configured (respects context cancellation)
	if m.SimulatedDelay > 0 && !m.AsyncMode {
		select {
		case <-ctx.Done():
			return Result{}, ErrVerificationCancelled
		case <-time.After(m.SimulatedDelay):
			// Continue after delay
		}
	}

	now := time.Now()
	var result Result

	if m.AsyncMode {
		// In async mode, return PENDING immediately
		result = Result{
			VerificationID: verificationID,
			Status:         StatusPending,
			Reason:         "Verification in progress",
			RiskScore:      0.0,
			CompletedAt:    nil,
			Metadata: map[string]string{
				"provider":     "mock",
				"party_id":     party.ID().String(),
				"party_type":   string(party.PartyType()),
				"submitted_at": now.Format(time.RFC3339),
			},
		}
	} else {
		// In sync mode, return final result immediately
		result = m.buildFinalResult(verificationID, party, now)
	}

	// Store the verification record
	m.mu.Lock()
	m.verifications[verificationID] = verificationRecord{
		result:    result,
		createdAt: now,
	}
	m.mu.Unlock()

	return result, nil
}

// CheckSanctions implements Provider.CheckSanctions.
func (m *MockProvider) CheckSanctions(ctx context.Context, party *domain.Party) (SanctionsResult, error) {
	// Generate deterministic screening ID
	screeningID := m.generateVerificationID("sanctions-" + party.ID().String())

	// Handle simulated delay if configured
	if m.SimulatedDelay > 0 {
		select {
		case <-ctx.Done():
			return SanctionsResult{}, ErrVerificationCancelled
		case <-time.After(m.SimulatedDelay):
			// Continue after delay
		}
	}

	now := time.Now()
	var result SanctionsResult

	if m.AlwaysApprove {
		result = SanctionsResult{
			ScreeningID: screeningID,
			Status:      SanctionsStatusClear,
			Matches:     nil,
			ScreenedAt:  now,
			Metadata: map[string]string{
				"provider":   "mock",
				"party_id":   party.ID().String(),
				"party_name": party.LegalName(),
			},
		}
	} else {
		// Simulate a potential match for rejected scenarios
		result = SanctionsResult{
			ScreeningID: screeningID,
			Status:      SanctionsStatusMatch,
			Matches: []SanctionsMatch{
				{
					ListName:        "MOCK_SANCTIONS_LIST",
					MatchedName:     party.LegalName(),
					MatchConfidence: 0.85,
					ListEntryID:     "MOCK-001",
				},
			},
			ScreenedAt: now,
			Metadata: map[string]string{
				"provider":   "mock",
				"party_id":   party.ID().String(),
				"party_name": party.LegalName(),
			},
		}
	}

	return result, nil
}

// GetVerificationStatus implements Provider.GetVerificationStatus.
func (m *MockProvider) GetVerificationStatus(_ context.Context, verificationID string) (Result, error) {
	m.mu.RLock()
	record, exists := m.verifications[verificationID]
	m.mu.RUnlock()

	if !exists {
		return Result{}, ErrVerificationNotFound
	}

	// In async mode, check if enough time has passed to complete
	if m.AsyncMode && record.result.Status == StatusPending {
		elapsed := time.Since(record.createdAt)
		if elapsed >= m.SimulatedDelay {
			// Time has passed, update to final status
			// Extract party_id from metadata to rebuild result
			partyID := record.result.Metadata["party_id"]
			now := time.Now()

			finalResult := m.buildFinalResultFromID(verificationID, partyID, now)

			// Update the stored record
			m.mu.Lock()
			m.verifications[verificationID] = verificationRecord{
				result:    finalResult,
				createdAt: record.createdAt,
			}
			m.mu.Unlock()

			return finalResult, nil
		}
	}

	return record.result, nil
}

// buildFinalResult creates the final verification result based on configuration.
func (m *MockProvider) buildFinalResult(verificationID string, party *domain.Party, completedAt time.Time) Result {
	var status Status
	var reason string
	var riskScore float64

	if m.AlwaysApprove {
		status = StatusApproved
		reason = "Identity verified successfully"
		riskScore = 0.1 // Low risk score for approved
	} else {
		status = StatusRejected
		reason = "Identity verification failed - document mismatch"
		riskScore = 0.9 // High risk score for rejected
	}

	return Result{
		VerificationID: verificationID,
		Status:         status,
		Reason:         reason,
		RiskScore:      riskScore,
		CompletedAt:    &completedAt,
		Metadata: map[string]string{
			"provider":     "mock",
			"party_id":     party.ID().String(),
			"party_type":   string(party.PartyType()),
			"completed_at": completedAt.Format(time.RFC3339),
		},
	}
}

// buildFinalResultFromID creates the final verification result using stored metadata.
func (m *MockProvider) buildFinalResultFromID(verificationID string, partyID string, completedAt time.Time) Result {
	var status Status
	var reason string
	var riskScore float64

	if m.AlwaysApprove {
		status = StatusApproved
		reason = "Identity verified successfully"
		riskScore = 0.1
	} else {
		status = StatusRejected
		reason = "Identity verification failed - document mismatch"
		riskScore = 0.9
	}

	return Result{
		VerificationID: verificationID,
		Status:         status,
		Reason:         reason,
		RiskScore:      riskScore,
		CompletedAt:    &completedAt,
		Metadata: map[string]string{
			"provider":     "mock",
			"party_id":     partyID,
			"completed_at": completedAt.Format(time.RFC3339),
		},
	}
}

// generateVerificationID creates a deterministic ID from the input.
// This ensures consistent IDs for testing.
func (m *MockProvider) generateVerificationID(input string) string {
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("mock-verify-%s", hex.EncodeToString(hash[:8]))
}

// Ensure MockProvider implements Provider
var _ Provider = (*MockProvider)(nil)
