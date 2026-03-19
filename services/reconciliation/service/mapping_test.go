package service

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToDomainReconciliationScope(t *testing.T) {
	tests := []struct {
		name  string
		proto reconciliationv1.ReconciliationScope
		want  domain.ReconciliationScope
	}{
		{
			name:  "account",
			proto: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			want:  domain.ReconciliationScopeAccount,
		},
		{
			name:  "instrument",
			proto: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT,
			want:  domain.ReconciliationScopeInstrument,
		},
		{
			name:  "portfolio",
			proto: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO,
			want:  domain.ReconciliationScopePortfolio,
		},
		{
			name:  "full",
			proto: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL,
			want:  domain.ReconciliationScopeFull,
		},
		{
			name:  "unspecified defaults to account",
			proto: reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED,
			want:  domain.ReconciliationScopeAccount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toDomainReconciliationScope(tt.proto)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToDomainSettlementType(t *testing.T) {
	tests := []struct {
		name  string
		proto reconciliationv1.SettlementType
		want  domain.SettlementType
	}{
		{
			name:  "daily",
			proto: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
			want:  domain.SettlementTypeDaily,
		},
		{
			name:  "weekly",
			proto: reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY,
			want:  domain.SettlementTypeWeekly,
		},
		{
			name:  "monthly",
			proto: reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY,
			want:  domain.SettlementTypeMonthly,
		},
		{
			name:  "on demand",
			proto: reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND,
			want:  domain.SettlementTypeOnDemand,
		},
		{
			name:  "end of day",
			proto: reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY,
			want:  domain.SettlementTypeEndOfDay,
		},
		{
			name:  "real time",
			proto: reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME,
			want:  domain.SettlementTypeRealTime,
		},
		{
			name:  "unspecified defaults to on demand",
			proto: reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED,
			want:  domain.SettlementTypeOnDemand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toDomainSettlementType(tt.proto)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToProtoRunStatus(t *testing.T) {
	tests := []struct {
		name   string
		domain domain.RunStatus
		want   reconciliationv1.RunStatus
	}{
		{
			name:   "pending",
			domain: domain.RunStatusPending,
			want:   reconciliationv1.RunStatus_RUN_STATUS_PENDING,
		},
		{
			name:   "running",
			domain: domain.RunStatusRunning,
			want:   reconciliationv1.RunStatus_RUN_STATUS_RUNNING,
		},
		{
			name:   "completed",
			domain: domain.RunStatusCompleted,
			want:   reconciliationv1.RunStatus_RUN_STATUS_COMPLETED,
		},
		{
			name:   "failed",
			domain: domain.RunStatusFailed,
			want:   reconciliationv1.RunStatus_RUN_STATUS_FAILED,
		},
		{
			name:   "cancelled",
			domain: domain.RunStatusCancelled,
			want:   reconciliationv1.RunStatus_RUN_STATUS_CANCELLED,
		},
		{
			name:   "finalized maps to completed",
			domain: domain.RunStatusFinalized,
			want:   reconciliationv1.RunStatus_RUN_STATUS_COMPLETED,
		},
		{
			name:   "unknown status returns unspecified",
			domain: domain.RunStatus("UNKNOWN"),
			want:   reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toProtoRunStatus(tt.domain)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToProtoReconciliationScope(t *testing.T) {
	tests := []struct {
		name   string
		domain domain.ReconciliationScope
		want   reconciliationv1.ReconciliationScope
	}{
		{
			name:   "account",
			domain: domain.ReconciliationScopeAccount,
			want:   reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
		},
		{
			name:   "instrument",
			domain: domain.ReconciliationScopeInstrument,
			want:   reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT,
		},
		{
			name:   "portfolio",
			domain: domain.ReconciliationScopePortfolio,
			want:   reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO,
		},
		{
			name:   "full",
			domain: domain.ReconciliationScopeFull,
			want:   reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL,
		},
		{
			name:   "unknown returns unspecified",
			domain: domain.ReconciliationScope("UNKNOWN"),
			want:   reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toProtoReconciliationScope(tt.domain)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToProtoSettlementType(t *testing.T) {
	tests := []struct {
		name   string
		domain domain.SettlementType
		want   reconciliationv1.SettlementType
	}{
		{
			name:   "daily",
			domain: domain.SettlementTypeDaily,
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
		},
		{
			name:   "weekly",
			domain: domain.SettlementTypeWeekly,
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY,
		},
		{
			name:   "monthly",
			domain: domain.SettlementTypeMonthly,
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY,
		},
		{
			name:   "on demand",
			domain: domain.SettlementTypeOnDemand,
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND,
		},
		{
			name:   "end of day",
			domain: domain.SettlementTypeEndOfDay,
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY,
		},
		{
			name:   "real time",
			domain: domain.SettlementTypeRealTime,
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME,
		},
		{
			name:   "final maps to on demand",
			domain: domain.SettlementTypeFinal,
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND,
		},
		{
			name:   "unknown returns unspecified",
			domain: domain.SettlementType("UNKNOWN"),
			want:   reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toProtoSettlementType(tt.domain)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToDomainVarianceStatus(t *testing.T) {
	tests := []struct {
		name  string
		proto reconciliationv1.VarianceStatus
		want  *domain.VarianceStatus
	}{
		{
			name:  "open",
			proto: reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN,
			want:  ptr(domain.VarianceStatusOpen),
		},
		{
			name:  "investigating",
			proto: reconciliationv1.VarianceStatus_VARIANCE_STATUS_INVESTIGATING,
			want:  ptr(domain.VarianceStatusInvestigating),
		},
		{
			name:  "disputed",
			proto: reconciliationv1.VarianceStatus_VARIANCE_STATUS_DISPUTED,
			want:  ptr(domain.VarianceStatusDisputed),
		},
		{
			name:  "resolved",
			proto: reconciliationv1.VarianceStatus_VARIANCE_STATUS_RESOLVED,
			want:  ptr(domain.VarianceStatusResolved),
		},
		{
			name:  "accepted",
			proto: reconciliationv1.VarianceStatus_VARIANCE_STATUS_ACCEPTED,
			want:  ptr(domain.VarianceStatusAccepted),
		},
		{
			name:  "unspecified returns nil",
			proto: reconciliationv1.VarianceStatus_VARIANCE_STATUS_UNSPECIFIED,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toDomainVarianceStatus(tt.proto)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func TestToDomainVarianceReason(t *testing.T) {
	tests := []struct {
		name  string
		proto reconciliationv1.VarianceReason
		want  *domain.VarianceReason
	}{
		{
			name:  "amount mismatch",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH,
			want:  ptrR(domain.VarianceReasonAmountMismatch),
		},
		{
			name:  "missing entry",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_MISSING_ENTRY,
			want:  ptrR(domain.VarianceReasonMissingEntry),
		},
		{
			name:  "duplicate entry",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_DUPLICATE_ENTRY,
			want:  ptrR(domain.VarianceReasonDuplicateEntry),
		},
		{
			name:  "timing difference",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_TIMING_DIFFERENCE,
			want:  ptrR(domain.VarianceReasonTimingDifference),
		},
		{
			name:  "currency mismatch",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_CURRENCY_MISMATCH,
			want:  ptrR(domain.VarianceReasonCurrencyMismatch),
		},
		{
			name:  "direction error",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_DIRECTION_ERROR,
			want:  ptrR(domain.VarianceReasonDirectionError),
		},
		{
			name:  "other",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER,
			want:  ptrR(domain.VarianceReasonOther),
		},
		{
			name:  "unspecified returns nil",
			proto: reconciliationv1.VarianceReason_VARIANCE_REASON_UNSPECIFIED,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toDomainVarianceReason(tt.proto)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func ptr(s domain.VarianceStatus) *domain.VarianceStatus  { return &s }
func ptrR(r domain.VarianceReason) *domain.VarianceReason { return &r }

func TestToProtoVarianceReason(t *testing.T) {
	tests := []struct {
		name   string
		domain domain.VarianceReason
		want   reconciliationv1.VarianceReason
	}{
		{
			name:   "amount mismatch",
			domain: domain.VarianceReasonAmountMismatch,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH,
		},
		{
			name:   "missing entry",
			domain: domain.VarianceReasonMissingEntry,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_MISSING_ENTRY,
		},
		{
			name:   "duplicate entry",
			domain: domain.VarianceReasonDuplicateEntry,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_DUPLICATE_ENTRY,
		},
		{
			name:   "timing difference",
			domain: domain.VarianceReasonTimingDifference,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_TIMING_DIFFERENCE,
		},
		{
			name:   "currency mismatch",
			domain: domain.VarianceReasonCurrencyMismatch,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_CURRENCY_MISMATCH,
		},
		{
			name:   "direction error",
			domain: domain.VarianceReasonDirectionError,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_DIRECTION_ERROR,
		},
		{
			name:   "other",
			domain: domain.VarianceReasonOther,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER,
		},
		{
			name:   "quality upgrade maps to other",
			domain: domain.VarianceReasonQualityUpgrade,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER,
		},
		{
			name:   "external mismatch maps to other",
			domain: domain.VarianceReasonExternalMismatch,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER,
		},
		{
			name:   "correction applied maps to other",
			domain: domain.VarianceReasonCorrectionApplied,
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER,
		},
		{
			name:   "unknown returns unspecified",
			domain: domain.VarianceReason("UNKNOWN"),
			want:   reconciliationv1.VarianceReason_VARIANCE_REASON_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toProtoVarianceReason(tt.domain)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToProtoVarianceStatus(t *testing.T) {
	tests := []struct {
		name   string
		domain domain.VarianceStatus
		want   reconciliationv1.VarianceStatus
	}{
		{
			name:   "open",
			domain: domain.VarianceStatusOpen,
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN,
		},
		{
			name:   "investigating",
			domain: domain.VarianceStatusInvestigating,
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_INVESTIGATING,
		},
		{
			name:   "disputed",
			domain: domain.VarianceStatusDisputed,
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_DISPUTED,
		},
		{
			name:   "resolved",
			domain: domain.VarianceStatusResolved,
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_RESOLVED,
		},
		{
			name:   "accepted",
			domain: domain.VarianceStatusAccepted,
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_ACCEPTED,
		},
		{
			name:   "detected maps to open",
			domain: domain.VarianceStatusDetected,
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN,
		},
		{
			name:   "valued maps to open",
			domain: domain.VarianceStatusValued,
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN,
		},
		{
			name:   "unknown returns unspecified",
			domain: domain.VarianceStatus("UNKNOWN"),
			want:   reconciliationv1.VarianceStatus_VARIANCE_STATUS_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toProtoVarianceStatus(tt.domain)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToProtoRunSummary(t *testing.T) {
	runID := uuid.New()
	now := time.Now().UTC()
	completedAt := now.Add(10 * time.Minute)

	run := &domain.SettlementRun{
		RunID:          runID,
		AccountID:      "acc-001",
		Scope:          domain.ReconciliationScopePortfolio,
		SettlementType: domain.SettlementTypeDaily,
		Status:         domain.RunStatusCompleted,
		PeriodStart:    now.Add(-24 * time.Hour),
		PeriodEnd:      now,
		InitiatedBy:    "system-user",
		CompletedAt:    &completedAt,
		VarianceCount:  5,
		FailureReason:  "",
		Attributes:     map[string]string{"env": "prod", "region": "eu-west-1"},
		CreatedAt:      now.Add(-1 * time.Hour),
		UpdatedAt:      now,
		Version:        3,
	}

	summary := toProtoRunSummary(run)
	require.NotNil(t, summary)

	assert.Equal(t, runID.String(), summary.RunId)
	assert.Equal(t, "acc-001", summary.AccountId)
	assert.Equal(t, reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO, summary.Scope)
	assert.Equal(t, reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY, summary.SettlementType)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_COMPLETED, summary.Status)
	assert.Equal(t, run.PeriodStart.Unix(), summary.PeriodStart.AsTime().Unix())
	assert.Equal(t, run.PeriodEnd.Unix(), summary.PeriodEnd.AsTime().Unix())
	assert.Equal(t, "system-user", summary.InitiatedBy)
	require.NotNil(t, summary.CompletedAt)
	assert.Equal(t, completedAt.Unix(), summary.CompletedAt.AsTime().Unix())
	assert.Equal(t, int32(5), summary.VarianceCount)
	assert.Equal(t, "", summary.FailureReason)
	assert.Equal(t, map[string]string{"env": "prod", "region": "eu-west-1"}, summary.Attributes)
	assert.Equal(t, run.CreatedAt.Unix(), summary.CreatedAt.AsTime().Unix())
	assert.Equal(t, run.UpdatedAt.Unix(), summary.UpdatedAt.AsTime().Unix())
	assert.Equal(t, int64(3), summary.Version)
}

func TestToProtoRunSummaryMinimal(t *testing.T) {
	runID := uuid.New()
	now := time.Now().UTC()

	run := &domain.SettlementRun{
		RunID:          runID,
		AccountID:      "acc-002",
		Scope:          domain.ReconciliationScopeAccount,
		SettlementType: domain.SettlementTypeOnDemand,
		Status:         domain.RunStatusPending,
		PeriodStart:    now.Add(-1 * time.Hour),
		PeriodEnd:      now,
		InitiatedBy:    "admin",
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}

	summary := toProtoRunSummary(run)
	require.NotNil(t, summary)

	assert.Equal(t, runID.String(), summary.RunId)
	assert.Nil(t, summary.CompletedAt, "completed_at should be nil for pending runs")
	assert.Equal(t, int32(0), summary.VarianceCount)
	assert.Empty(t, summary.FailureReason)
	assert.Nil(t, summary.Attributes, "nil attributes should result in nil map")
}

func TestToProtoRunSummaryNil(t *testing.T) {
	assert.Nil(t, toProtoRunSummary(nil))
}

func TestToProtoVarianceDetail(t *testing.T) {
	varianceID := uuid.New()
	runID := uuid.New()
	snapshotID := uuid.New()
	now := time.Now().UTC()
	resolvedAt := now.Add(5 * time.Minute)

	v := &domain.Variance{
		VarianceID:     varianceID,
		RunID:          runID,
		SnapshotID:     snapshotID,
		AccountID:      "acc-001",
		InstrumentCode: "GBP",
		ExpectedAmount: decimal.NewFromFloat(1000.50),
		ActualAmount:   decimal.NewFromFloat(999.25),
		VarianceAmount: decimal.NewFromFloat(-1.25),
		Reason:         domain.VarianceReasonAmountMismatch,
		Status:         domain.VarianceStatusResolved,
		ResolutionNote: "Rounding difference accepted",
		ResolvedBy:     "auditor-1",
		ResolvedAt:     &resolvedAt,
		Attributes:     map[string]string{"source": "stripe", "batch": "20260210"},
		CreatedAt:      now.Add(-10 * time.Minute),
		UpdatedAt:      now,
	}

	detail := toProtoVarianceDetail(v)
	require.NotNil(t, detail)

	assert.Equal(t, varianceID.String(), detail.VarianceId)
	assert.Equal(t, runID.String(), detail.RunId)
	assert.Equal(t, snapshotID.String(), detail.SnapshotId)
	assert.Equal(t, "acc-001", detail.AccountId)
	assert.Equal(t, "GBP", detail.InstrumentCode)
	assert.Equal(t, "1000.5", detail.ExpectedAmount)
	assert.Equal(t, "999.25", detail.ActualAmount)
	assert.Equal(t, "-1.25", detail.VarianceAmount)
	assert.Equal(t, reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH, detail.Reason)
	assert.Equal(t, reconciliationv1.VarianceStatus_VARIANCE_STATUS_RESOLVED, detail.Status)
	assert.Equal(t, "Rounding difference accepted", detail.ResolutionNote)
	assert.Equal(t, "auditor-1", detail.ResolvedBy)
	require.NotNil(t, detail.ResolvedAt)
	assert.Equal(t, resolvedAt.Unix(), detail.ResolvedAt.AsTime().Unix())
	assert.Equal(t, map[string]string{"source": "stripe", "batch": "20260210"}, detail.Attributes)
	assert.Equal(t, v.CreatedAt.Unix(), detail.CreatedAt.AsTime().Unix())
	assert.Equal(t, v.UpdatedAt.Unix(), detail.UpdatedAt.AsTime().Unix())
}

func TestToProtoVarianceDetailMinimal(t *testing.T) {
	varianceID := uuid.New()
	runID := uuid.New()
	snapshotID := uuid.New()
	now := time.Now().UTC()

	v := &domain.Variance{
		VarianceID:     varianceID,
		RunID:          runID,
		SnapshotID:     snapshotID,
		AccountID:      "acc-002",
		InstrumentCode: "KWH",
		ExpectedAmount: decimal.NewFromInt(500),
		ActualAmount:   decimal.NewFromInt(500),
		VarianceAmount: decimal.Zero,
		Reason:         domain.VarianceReasonOther,
		Status:         domain.VarianceStatusOpen,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	detail := toProtoVarianceDetail(v)
	require.NotNil(t, detail)

	assert.Empty(t, detail.ResolutionNote)
	assert.Empty(t, detail.ResolvedBy)
	assert.Nil(t, detail.ResolvedAt, "resolved_at should be nil for unresolved variances")
	assert.Nil(t, detail.Attributes, "nil attributes should result in nil map")
}

func TestToProtoVarianceDetailNil(t *testing.T) {
	assert.Nil(t, toProtoVarianceDetail(nil))
}

func TestEncodeCursor(t *testing.T) {
	encoded := encodeCursor(42)
	assert.NotEmpty(t, encoded)

	decoded, err := decodeCursor(encoded)
	require.NoError(t, err)
	assert.Equal(t, 42, decoded)
}

func TestEncodeCursorZero(t *testing.T) {
	encoded := encodeCursor(0)
	decoded, err := decodeCursor(encoded)
	require.NoError(t, err)
	assert.Equal(t, 0, decoded)
}

func TestDecodeCursorEmpty(t *testing.T) {
	offset, err := decodeCursor("")
	require.NoError(t, err)
	assert.Equal(t, 0, offset)
}

func TestDecodeCursorInvalid(t *testing.T) {
	_, err := decodeCursor("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDecodeCursorInvalidContent(t *testing.T) {
	// Valid base64 but not a number
	_, err := decodeCursor("aGVsbG8=") // "hello" in base64
	assert.Error(t, err)
}

func TestDecodeCursorNegativeOffset(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("-5"))
	_, err := decodeCursor(encoded)
	assert.Error(t, err)
}

func TestToDomainRunStatus(t *testing.T) {
	tests := []struct {
		name  string
		proto reconciliationv1.RunStatus
		want  domain.RunStatus
	}{
		{"unspecified defaults to pending", reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED, domain.RunStatusPending},
		{"pending", reconciliationv1.RunStatus_RUN_STATUS_PENDING, domain.RunStatusPending},
		{"running", reconciliationv1.RunStatus_RUN_STATUS_RUNNING, domain.RunStatusRunning},
		{"completed", reconciliationv1.RunStatus_RUN_STATUS_COMPLETED, domain.RunStatusCompleted},
		{"failed", reconciliationv1.RunStatus_RUN_STATUS_FAILED, domain.RunStatusFailed},
		{"cancelled", reconciliationv1.RunStatus_RUN_STATUS_CANCELLED, domain.RunStatusCancelled},
		{"paused", reconciliationv1.RunStatus_RUN_STATUS_PAUSED, domain.RunStatusPaused},
		{"unknown defaults to pending", reconciliationv1.RunStatus(999), domain.RunStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toDomainRunStatus(tt.proto)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToDomainDisputeStatusFilter(t *testing.T) {
	tests := []struct {
		name  string
		proto reconciliationv1.DisputeStatus
		want  *domain.DisputeStatus
	}{
		{"open", reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN, ptrD(domain.DisputeStatusOpen)},
		{"under review", reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNDER_REVIEW, ptrD(domain.DisputeStatusUnderReview)},
		{"escalated", reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED, ptrD(domain.DisputeStatusEscalated)},
		{"resolved", reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED, ptrD(domain.DisputeStatusResolved)},
		{"rejected", reconciliationv1.DisputeStatus_DISPUTE_STATUS_REJECTED, ptrD(domain.DisputeStatusRejected)},
		{"unspecified returns nil", reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNSPECIFIED, nil},
		{"unknown returns nil", reconciliationv1.DisputeStatus(999), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toDomainDisputeStatusFilter(tt.proto)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func ptrD(s domain.DisputeStatus) *domain.DisputeStatus { return &s }
