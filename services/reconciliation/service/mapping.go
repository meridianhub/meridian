package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var errNegativeCursorOffset = errors.New("invalid cursor: negative offset")

// toDomainReconciliationScope converts a proto ReconciliationScope to domain.
// UNSPECIFIED defaults to ACCOUNT.
func toDomainReconciliationScope(s reconciliationv1.ReconciliationScope) domain.ReconciliationScope {
	switch s {
	case reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT:
		return domain.ReconciliationScopeAccount
	case reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT:
		return domain.ReconciliationScopeInstrument
	case reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO:
		return domain.ReconciliationScopePortfolio
	case reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL:
		return domain.ReconciliationScopeFull
	case reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED:
		return domain.ReconciliationScopeAccount
	}
	return domain.ReconciliationScopeAccount
}

// toDomainSettlementType converts a proto SettlementType to domain.
// UNSPECIFIED defaults to ON_DEMAND.
func toDomainSettlementType(s reconciliationv1.SettlementType) domain.SettlementType {
	switch s {
	case reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY:
		return domain.SettlementTypeDaily
	case reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY:
		return domain.SettlementTypeWeekly
	case reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY:
		return domain.SettlementTypeMonthly
	case reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND:
		return domain.SettlementTypeOnDemand
	case reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY:
		return domain.SettlementTypeEndOfDay
	case reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME:
		return domain.SettlementTypeRealTime
	case reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED:
		return domain.SettlementTypeOnDemand
	}
	return domain.SettlementTypeOnDemand
}

// toProtoRunStatus converts a domain RunStatus to proto.
func toProtoRunStatus(s domain.RunStatus) reconciliationv1.RunStatus {
	switch s {
	case domain.RunStatusPending:
		return reconciliationv1.RunStatus_RUN_STATUS_PENDING
	case domain.RunStatusRunning:
		return reconciliationv1.RunStatus_RUN_STATUS_RUNNING
	case domain.RunStatusPaused:
		return reconciliationv1.RunStatus_RUN_STATUS_PAUSED
	case domain.RunStatusCompleted, domain.RunStatusFinalized:
		return reconciliationv1.RunStatus_RUN_STATUS_COMPLETED
	case domain.RunStatusFailed:
		return reconciliationv1.RunStatus_RUN_STATUS_FAILED
	case domain.RunStatusCancelled:
		return reconciliationv1.RunStatus_RUN_STATUS_CANCELLED
	}
	return reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED
}

// toDomainRunStatus converts a proto RunStatus to domain.
// UNSPECIFIED returns RunStatusPending as default.
func toDomainRunStatus(s reconciliationv1.RunStatus) domain.RunStatus {
	switch s {
	case reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED:
		return domain.RunStatusPending
	case reconciliationv1.RunStatus_RUN_STATUS_PENDING:
		return domain.RunStatusPending
	case reconciliationv1.RunStatus_RUN_STATUS_RUNNING:
		return domain.RunStatusRunning
	case reconciliationv1.RunStatus_RUN_STATUS_COMPLETED:
		return domain.RunStatusCompleted
	case reconciliationv1.RunStatus_RUN_STATUS_FAILED:
		return domain.RunStatusFailed
	case reconciliationv1.RunStatus_RUN_STATUS_CANCELLED:
		return domain.RunStatusCancelled
	case reconciliationv1.RunStatus_RUN_STATUS_PAUSED:
		return domain.RunStatusPaused
	}
	return domain.RunStatusPending
}

// toProtoReconciliationScope converts a domain ReconciliationScope to proto.
func toProtoReconciliationScope(s domain.ReconciliationScope) reconciliationv1.ReconciliationScope {
	switch s {
	case domain.ReconciliationScopeAccount:
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT
	case domain.ReconciliationScopeInstrument:
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT
	case domain.ReconciliationScopePortfolio:
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO
	case domain.ReconciliationScopeFull:
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL
	default:
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED
	}
}

// toProtoSettlementType converts a domain SettlementType to proto.
func toProtoSettlementType(s domain.SettlementType) reconciliationv1.SettlementType {
	switch s {
	case domain.SettlementTypeDaily:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY
	case domain.SettlementTypeWeekly:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY
	case domain.SettlementTypeMonthly:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY
	case domain.SettlementTypeOnDemand:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND
	case domain.SettlementTypeEndOfDay:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY
	case domain.SettlementTypeRealTime:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME
	case domain.SettlementTypeFinal:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND
	}
	return reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED
}

// toDomainVarianceStatus converts a proto VarianceStatus to domain.
// UNSPECIFIED returns nil (no filter).
func toDomainVarianceStatus(s reconciliationv1.VarianceStatus) *domain.VarianceStatus {
	var status domain.VarianceStatus
	switch s {
	case reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN:
		status = domain.VarianceStatusOpen
	case reconciliationv1.VarianceStatus_VARIANCE_STATUS_INVESTIGATING:
		status = domain.VarianceStatusInvestigating
	case reconciliationv1.VarianceStatus_VARIANCE_STATUS_DISPUTED:
		status = domain.VarianceStatusDisputed
	case reconciliationv1.VarianceStatus_VARIANCE_STATUS_RESOLVED:
		status = domain.VarianceStatusResolved
	case reconciliationv1.VarianceStatus_VARIANCE_STATUS_ACCEPTED:
		status = domain.VarianceStatusAccepted
	case reconciliationv1.VarianceStatus_VARIANCE_STATUS_UNSPECIFIED:
		return nil
	default:
		return nil
	}
	return &status
}

// toDomainVarianceReason converts a proto VarianceReason to domain.
// UNSPECIFIED returns nil (no filter).
func toDomainVarianceReason(s reconciliationv1.VarianceReason) *domain.VarianceReason {
	var reason domain.VarianceReason
	switch s {
	case reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH:
		reason = domain.VarianceReasonAmountMismatch
	case reconciliationv1.VarianceReason_VARIANCE_REASON_MISSING_ENTRY:
		reason = domain.VarianceReasonMissingEntry
	case reconciliationv1.VarianceReason_VARIANCE_REASON_DUPLICATE_ENTRY:
		reason = domain.VarianceReasonDuplicateEntry
	case reconciliationv1.VarianceReason_VARIANCE_REASON_TIMING_DIFFERENCE:
		reason = domain.VarianceReasonTimingDifference
	case reconciliationv1.VarianceReason_VARIANCE_REASON_CURRENCY_MISMATCH:
		reason = domain.VarianceReasonCurrencyMismatch
	case reconciliationv1.VarianceReason_VARIANCE_REASON_DIRECTION_ERROR:
		reason = domain.VarianceReasonDirectionError
	case reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER:
		reason = domain.VarianceReasonOther
	case reconciliationv1.VarianceReason_VARIANCE_REASON_UNSPECIFIED:
		return nil
	default:
		return nil
	}
	return &reason
}

// toProtoVarianceReason converts a domain VarianceReason to proto.
func toProtoVarianceReason(r domain.VarianceReason) reconciliationv1.VarianceReason {
	switch r {
	case domain.VarianceReasonAmountMismatch:
		return reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH
	case domain.VarianceReasonMissingEntry:
		return reconciliationv1.VarianceReason_VARIANCE_REASON_MISSING_ENTRY
	case domain.VarianceReasonDuplicateEntry:
		return reconciliationv1.VarianceReason_VARIANCE_REASON_DUPLICATE_ENTRY
	case domain.VarianceReasonTimingDifference:
		return reconciliationv1.VarianceReason_VARIANCE_REASON_TIMING_DIFFERENCE
	case domain.VarianceReasonCurrencyMismatch:
		return reconciliationv1.VarianceReason_VARIANCE_REASON_CURRENCY_MISMATCH
	case domain.VarianceReasonDirectionError:
		return reconciliationv1.VarianceReason_VARIANCE_REASON_DIRECTION_ERROR
	case domain.VarianceReasonOther, domain.VarianceReasonQualityUpgrade,
		domain.VarianceReasonExternalMismatch, domain.VarianceReasonCorrectionApplied:
		return reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER
	}
	return reconciliationv1.VarianceReason_VARIANCE_REASON_UNSPECIFIED
}

// toProtoVarianceStatus converts a domain VarianceStatus to proto.
func toProtoVarianceStatus(s domain.VarianceStatus) reconciliationv1.VarianceStatus {
	switch s {
	case domain.VarianceStatusDetected, domain.VarianceStatusValued,
		domain.VarianceStatusOpen:
		return reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN
	case domain.VarianceStatusInvestigating:
		return reconciliationv1.VarianceStatus_VARIANCE_STATUS_INVESTIGATING
	case domain.VarianceStatusDisputed:
		return reconciliationv1.VarianceStatus_VARIANCE_STATUS_DISPUTED
	case domain.VarianceStatusResolved:
		return reconciliationv1.VarianceStatus_VARIANCE_STATUS_RESOLVED
	case domain.VarianceStatusAccepted:
		return reconciliationv1.VarianceStatus_VARIANCE_STATUS_ACCEPTED
	}
	return reconciliationv1.VarianceStatus_VARIANCE_STATUS_UNSPECIFIED
}

// toProtoRunSummary converts a domain SettlementRun to a proto SettlementRunSummary.
func toProtoRunSummary(run *domain.SettlementRun) *reconciliationv1.SettlementRunSummary {
	if run == nil {
		return nil
	}

	summary := &reconciliationv1.SettlementRunSummary{
		RunId:          run.RunID.String(),
		AccountId:      run.AccountID,
		Scope:          toProtoReconciliationScope(run.Scope),
		SettlementType: toProtoSettlementType(run.SettlementType),
		Status:         toProtoRunStatus(run.Status),
		PeriodStart:    timestamppb.New(run.PeriodStart),
		PeriodEnd:      timestamppb.New(run.PeriodEnd),
		InitiatedBy:    run.InitiatedBy,
		VarianceCount:  int32(run.VarianceCount),
		FailureReason:  run.FailureReason,
		Attributes:     run.Attributes,
		CreatedAt:      timestamppb.New(run.CreatedAt),
		UpdatedAt:      timestamppb.New(run.UpdatedAt),
		Version:        run.Version,
	}

	if run.CompletedAt != nil {
		summary.CompletedAt = timestamppb.New(*run.CompletedAt)
	}

	return summary
}

// toProtoVarianceDetail converts a domain Variance to a proto VarianceDetail.
func toProtoVarianceDetail(v *domain.Variance) *reconciliationv1.VarianceDetail {
	if v == nil {
		return nil
	}

	detail := &reconciliationv1.VarianceDetail{
		VarianceId:     v.VarianceID.String(),
		RunId:          v.RunID.String(),
		SnapshotId:     v.SnapshotID.String(),
		AccountId:      v.AccountID,
		InstrumentCode: v.InstrumentCode,
		ExpectedAmount: v.ExpectedAmount.String(),
		ActualAmount:   v.ActualAmount.String(),
		VarianceAmount: v.VarianceAmount.String(),
		Reason:         toProtoVarianceReason(v.Reason),
		Status:         toProtoVarianceStatus(v.Status),
		ResolutionNote: v.ResolutionNote,
		ResolvedBy:     v.ResolvedBy,
		Attributes:     v.Attributes,
		CreatedAt:      timestamppb.New(v.CreatedAt),
		UpdatedAt:      timestamppb.New(v.UpdatedAt),
	}

	if v.ResolvedAt != nil {
		detail.ResolvedAt = timestamppb.New(*v.ResolvedAt)
	}

	return detail
}

// toProtoAssertionDetail converts a domain BalanceAssertion to proto.
func toProtoAssertionDetail(a *domain.BalanceAssertion) *reconciliationv1.BalanceAssertionDetail {
	if a == nil {
		return nil
	}

	detail := &reconciliationv1.BalanceAssertionDetail{
		AssertionId:     a.AssertionID.String(),
		AccountId:       a.AccountID,
		InstrumentCode:  a.InstrumentCode,
		Expression:      a.Expression,
		ExpectedBalance: a.ExpectedBalance.String(),
		ActualBalance:   a.ActualBalance.String(),
		Status:          toProtoAssertionStatus(a.Status),
		FailureReason:   a.FailureReason,
		OverrideReason:  a.OverrideReason,
		CreatedAt:       timestamppb.New(a.CreatedAt),
	}

	if a.RunID != nil {
		detail.RunId = a.RunID.String()
	}

	if !a.AssertedAt.IsZero() {
		detail.AssertedAt = timestamppb.New(a.AssertedAt)
	}

	return detail
}

// toProtoAssertionStatus converts domain AssertionStatus to proto enum.
func toProtoAssertionStatus(s domain.AssertionStatus) reconciliationv1.AssertionStatus {
	switch s {
	case domain.AssertionStatusPending:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_PENDING
	case domain.AssertionStatusPassed:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_PASSED
	case domain.AssertionStatusFailed:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_FAILED
	case domain.AssertionStatusOverride:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_OVERRIDE
	default:
		return reconciliationv1.AssertionStatus_ASSERTION_STATUS_UNSPECIFIED
	}
}

// encodeCursor encodes an offset as a base64 page token.
func encodeCursor(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeCursor decodes a base64 page token to an offset.
// An empty token returns offset 0.
func decodeCursor(token string) (int, error) {
	if token == "" {
		return 0, nil
	}

	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor: %w", err)
	}

	offset, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("invalid cursor content: %w", err)
	}

	if offset < 0 {
		return 0, errNegativeCursorOffset
	}

	return offset, nil
}
