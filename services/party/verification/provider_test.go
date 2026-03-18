package verification

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
)

func createTestParty(t *testing.T) *domain.Party {
	t.Helper()
	party, err := domain.NewParty(domain.PartyTypePerson, "John Doe")
	require.NoError(t, err)
	return party
}

func TestMockProvider_AlwaysApprove_ReturnsApproved(t *testing.T) {
	// Arrange
	provider := NewMockProvider().WithAlwaysApprove(true)
	party := createTestParty(t)
	ctx := context.Background()

	// Act
	result, err := provider.VerifyIdentity(ctx, party)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, result.Status)
	assert.NotEmpty(t, result.VerificationID)
	assert.Equal(t, "Identity verified successfully", result.Reason)
	assert.InDelta(t, 0.1, result.RiskScore, 0.001)
	assert.NotNil(t, result.CompletedAt)
}

func TestMockProvider_AlwaysFalse_ReturnsRejected(t *testing.T) {
	// Arrange
	provider := NewMockProvider().WithAlwaysApprove(false)
	party := createTestParty(t)
	ctx := context.Background()

	// Act
	result, err := provider.VerifyIdentity(ctx, party)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, result.Status)
	assert.NotEmpty(t, result.VerificationID)
	assert.Contains(t, result.Reason, "failed")
	assert.InDelta(t, 0.9, result.RiskScore, 0.001)
}

func TestResult_Validate_RejectsInvalidRiskScore(t *testing.T) {
	testCases := []struct {
		name      string
		riskScore float64
		expectErr bool
	}{
		{"valid zero", 0.0, false},
		{"valid one", 1.0, false},
		{"valid middle", 0.5, false},
		{"invalid negative", -0.1, true},
		{"invalid above one", 1.1, true},
		{"invalid large negative", -100.0, true},
		{"invalid large positive", 100.0, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := Result{
				VerificationID: "test-id",
				Status:         StatusApproved,
				RiskScore:      tc.riskScore,
			}

			err := result.Validate()

			if tc.expectErr {
				assert.ErrorIs(t, err, ErrInvalidRiskScore)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMockProvider_SimulatedDelay_RespectsDelay(t *testing.T) {
	// Arrange
	delay := 100 * time.Millisecond
	provider := NewMockProvider().
		WithAlwaysApprove(true).
		WithSimulatedDelay(delay)
	party := createTestParty(t)
	ctx := context.Background()

	// Act
	start := time.Now()
	result, err := provider.VerifyIdentity(ctx, party)
	elapsed := time.Since(start)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, result.Status)
	// Verify that at least the delay duration passed
	assert.GreaterOrEqual(t, elapsed, delay)
}

func TestMockProvider_SimulatedDelay_RespectsContextCancellation(t *testing.T) {
	// Arrange
	delay := 5 * time.Second // Long delay
	provider := NewMockProvider().
		WithAlwaysApprove(true).
		WithSimulatedDelay(delay)
	party := createTestParty(t)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context after a short time
	go func() {
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo // triggers context cancellation mid-verification
		cancel()
	}()

	// Act
	start := time.Now()
	_, err := provider.VerifyIdentity(ctx, party)
	elapsed := time.Since(start)

	// Assert
	assert.ErrorIs(t, err, ErrVerificationCancelled)
	// Should return quickly after cancellation, not wait full delay
	assert.Less(t, elapsed, delay)
}

func TestMockProvider_GetVerificationStatus_ReturnsConsistentResults(t *testing.T) {
	// Arrange
	provider := NewMockProvider().WithAlwaysApprove(true)
	party := createTestParty(t)
	ctx := context.Background()

	// Act - verify the party first
	initialResult, err := provider.VerifyIdentity(ctx, party)
	require.NoError(t, err)

	// Act - get status multiple times
	status1, err := provider.GetVerificationStatus(ctx, initialResult.VerificationID)
	require.NoError(t, err)

	status2, err := provider.GetVerificationStatus(ctx, initialResult.VerificationID)
	require.NoError(t, err)

	// Assert - results should be consistent
	assert.Equal(t, status1.VerificationID, status2.VerificationID)
	assert.Equal(t, status1.Status, status2.Status)
	assert.Equal(t, status1.RiskScore, status2.RiskScore)
}

func TestMockProvider_GetVerificationStatus_ReturnsNotFound(t *testing.T) {
	// Arrange
	provider := NewMockProvider()
	ctx := context.Background()

	// Act
	_, err := provider.GetVerificationStatus(ctx, "nonexistent-id")

	// Assert
	assert.ErrorIs(t, err, ErrVerificationNotFound)
}

func TestMockProvider_AsyncMode_ReturnsPendingThenCompletes(t *testing.T) {
	// Arrange
	delay := 50 * time.Millisecond
	provider := NewMockProvider().
		WithAlwaysApprove(true).
		WithAsyncMode(true).
		WithSimulatedDelay(delay)
	party := createTestParty(t)
	ctx := context.Background()

	// Act - initial verification should return PENDING immediately
	result, err := provider.VerifyIdentity(ctx, party)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
	assert.Nil(t, result.CompletedAt)

	// Use await to poll for completion instead of time.Sleep
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			status, err := provider.GetVerificationStatus(ctx, result.VerificationID)
			if err != nil {
				return false
			}
			return status.Status == StatusApproved
		})

	require.NoError(t, err, "verification should complete within timeout")

	// Verify final status
	finalStatus, err := provider.GetVerificationStatus(ctx, result.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, finalStatus.Status)
	assert.NotNil(t, finalStatus.CompletedAt)
}

func TestMockProvider_CheckSanctions_Clear(t *testing.T) {
	// Arrange
	provider := NewMockProvider().WithAlwaysApprove(true)
	party := createTestParty(t)
	ctx := context.Background()

	// Act
	result, err := provider.CheckSanctions(ctx, party)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, SanctionsStatusClear, result.Status)
	assert.Empty(t, result.Matches)
	assert.NotEmpty(t, result.ScreeningID)
}

func TestMockProvider_CheckSanctions_Match(t *testing.T) {
	// Arrange
	provider := NewMockProvider().WithAlwaysApprove(false)
	party := createTestParty(t)
	ctx := context.Background()

	// Act
	result, err := provider.CheckSanctions(ctx, party)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, SanctionsStatusMatch, result.Status)
	assert.Len(t, result.Matches, 1)
	assert.Equal(t, "MOCK_SANCTIONS_LIST", result.Matches[0].ListName)
}

func TestMockProvider_DeterministicVerificationID(t *testing.T) {
	// Arrange
	provider := NewMockProvider()
	party := createTestParty(t)
	ctx := context.Background()

	// Act - verify the same party twice with different providers
	result1, err := provider.VerifyIdentity(ctx, party)
	require.NoError(t, err)

	// Create new provider instance
	provider2 := NewMockProvider()
	result2, err := provider2.VerifyIdentity(ctx, party)
	require.NoError(t, err)

	// Assert - same party should produce same verification ID
	assert.Equal(t, result1.VerificationID, result2.VerificationID)
}

func TestStatus_IsValid(t *testing.T) {
	testCases := []struct {
		status Status
		valid  bool
	}{
		{StatusPending, true},
		{StatusApproved, true},
		{StatusRejected, true},
		{StatusManualReview, true},
		{Status("INVALID"), false},
		{Status(""), false},
	}

	for _, tc := range testCases {
		t.Run(string(tc.status), func(t *testing.T) {
			assert.Equal(t, tc.valid, tc.status.IsValid())
		})
	}
}

func TestSanctionsStatus_IsValid(t *testing.T) {
	testCases := []struct {
		status SanctionsStatus
		valid  bool
	}{
		{SanctionsStatusClear, true},
		{SanctionsStatusMatch, true},
		{SanctionsStatusPending, true},
		{SanctionsStatusError, true},
		{SanctionsStatus("INVALID"), false},
		{SanctionsStatus(""), false},
	}

	for _, tc := range testCases {
		t.Run(string(tc.status), func(t *testing.T) {
			assert.Equal(t, tc.valid, tc.status.IsValid())
		})
	}
}

func TestSanctionsResult_Validate(t *testing.T) {
	t.Run("valid result", func(t *testing.T) {
		result := SanctionsResult{
			ScreeningID: "test-id",
			Status:      SanctionsStatusClear,
			ScreenedAt:  time.Now(),
		}
		assert.NoError(t, result.Validate())
	})

	t.Run("invalid status", func(t *testing.T) {
		result := SanctionsResult{
			ScreeningID: "test-id",
			Status:      SanctionsStatus("INVALID"),
			ScreenedAt:  time.Now(),
		}
		assert.Error(t, result.Validate())
	})
}
