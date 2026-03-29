package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// CachedInstrument contains the instrument definition and precompiled CEL programs.
// This is a local type that mirrors the reference-data cache type to avoid
// circular dependencies.
type CachedInstrument struct {
	// InstrumentCode is the unique code for the instrument.
	InstrumentCode string

	// ValidationProgram is the precompiled CEL program for validation.
	// May be nil if no validation expression is defined.
	ValidationProgram cel.Program

	// BucketKeyProgram is the precompiled CEL program for bucket key generation.
	// May be nil if no bucket key expression is defined.
	// When evaluated, returns a SHA256 hex string (64 characters) representing
	// the bucket/fungibility key for the measurement.
	BucketKeyProgram cel.Program
}

// InstrumentCache provides an interface for looking up instrument definitions
// with precompiled CEL validation programs.
//
// This interface allows the position-keeping service to validate measurements
// against instrument definitions without depending directly on the reference-data
// service implementation.
type InstrumentCache interface {
	// GetOrLoad retrieves a cached instrument or loads it via loadFn on cache miss.
	// The loadFn should load from the repository and compile CEL programs as needed.
	// Returns the cached instrument or an error if loading fails.
	GetOrLoad(ctx context.Context, code string, version int, loadFn func() (*CachedInstrument, error)) (*CachedInstrument, error)
}

// BucketCounter provides an interface for counting buckets per account/instrument.
// This is used to enforce cardinality limits and prevent "Infinite Buckets" DOS attacks.
//
// The cardinality limit protects against malicious or misconfigured instruments that
// could create unbounded numbers of buckets, consuming excessive storage and degrading
// query performance.
type BucketCounter interface {
	// CountBuckets returns the number of distinct buckets for an account and instrument.
	// Returns the count and any error encountered during the query.
	CountBuckets(ctx context.Context, accountID string, instrumentCode string) (int, error)

	// BucketExists checks whether a specific bucket already exists for the given account, instrument, and bucket ID.
	// Used to distinguish new buckets from existing ones during cardinality enforcement.
	BucketExists(ctx context.Context, accountID string, instrumentCode string, bucketID string) (bool, error)
}

// ErrEmptyUUID is returned when UUID string is empty
var ErrEmptyUUID = errors.New("UUID string is empty")

// ErrInstrumentNotFound is returned when an instrument is not found in the cache.
var ErrInstrumentNotFound = errors.New("instrument not found")

// toProtoFinancialPositionLog converts a domain FinancialPositionLog to its protobuf representation.
func toProtoFinancialPositionLog(log *domain.FinancialPositionLog) *positionkeepingv1.FinancialPositionLog {
	if log == nil {
		return nil
	}

	proto := &positionkeepingv1.FinancialPositionLog{
		LogId:                 log.LogID.String(),
		AccountId:             log.AccountID,
		AccountServiceDomain:  toProtoAccountServiceDomain(log.AccountServiceDomain),
		TransactionLogEntries: make([]*positionkeepingv1.TransactionLogEntry, 0, len(log.TransactionLogEntries)),
		TransactionLineage:    toProtoTransactionLineage(log.TransactionLineage),
		AuditTrail:            make([]*positionkeepingv1.AuditTrailEntry, 0, len(log.AuditTrail)),
		StatusTracking:        toProtoStatusTracking(log.StatusTracking),
		CreatedAt:             timestamppb.New(log.CreatedAt),
		UpdatedAt:             timestamppb.New(log.UpdatedAt),
		Version:               log.Version,
	}

	// Convert transaction log entries
	for _, entry := range log.TransactionLogEntries {
		proto.TransactionLogEntries = append(proto.TransactionLogEntries, toProtoTransactionLogEntry(entry))
	}

	// Convert audit trail entries
	for _, entry := range log.AuditTrail {
		proto.AuditTrail = append(proto.AuditTrail, toProtoAuditTrailEntry(entry))
	}

	return proto
}

// toProtoTransactionLogEntry converts a domain TransactionLogEntry to protobuf.
func toProtoTransactionLogEntry(entry *domain.TransactionLogEntry) *positionkeepingv1.TransactionLogEntry {
	if entry == nil {
		return nil
	}

	return &positionkeepingv1.TransactionLogEntry{
		EntryId:       entry.EntryID.String(),
		TransactionId: entry.TransactionID.String(),
		AccountId:     entry.AccountID,
		Amount:        toProtoMoneyAmount(entry.Amount),
		Direction:     toProtoPostingDirection(entry.Direction),
		Timestamp:     timestamppb.New(entry.Timestamp),
		Description:   entry.Description,
		Reference:     entry.Reference,
	}
}

// toProtoMoneyAmount converts a domain Money to protobuf MoneyAmount.
func toProtoMoneyAmount(domainMoney domain.Money) *commonv1.MoneyAmount {
	// Convert decimal to units and nanos
	// For example: 123.456789 GBP -> units: 123, nanos: 456789000
	amount := domainMoney.Amount
	units := amount.IntPart()
	fraction := amount.Sub(amount.Truncate(0))
	nanos := fraction.Mul(decimal.NewFromInt(1000000000)).IntPart()

	// Clamp nanos to int32 range to prevent overflow
	if nanos > 999999999 {
		nanos = 999999999
	} else if nanos < -999999999 {
		nanos = -999999999
	}

	return &commonv1.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: string(domain.MoneyCurrency(domainMoney)),
			Units:        units,
			Nanos:        int32(nanos), // #nosec G115 -- Safely clamped to int32 range above
		},
	}
}

// toProtoPostingDirection converts a domain PostingDirection to protobuf.
func toProtoPostingDirection(direction domain.PostingDirection) commonv1.PostingDirection {
	switch direction {
	case domain.PostingDirectionDebit:
		return commonv1.PostingDirection_POSTING_DIRECTION_DEBIT
	case domain.PostingDirectionCredit:
		return commonv1.PostingDirection_POSTING_DIRECTION_CREDIT
	default:
		return commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED
	}
}

// toProtoTransactionLineage converts a domain TransactionLineage to protobuf.
func toProtoTransactionLineage(lineage *domain.TransactionLineage) *positionkeepingv1.TransactionLineage {
	if lineage == nil {
		return nil
	}

	proto := &positionkeepingv1.TransactionLineage{
		TransactionId:   lineage.TransactionID().String(),
		TransactionType: lineage.TransactionType(),
		CreatedAt:       timestamppb.New(lineage.CreatedAt()),
	}

	// Set parent transaction ID if present
	if lineage.ParentTransactionID() != nil {
		proto.ParentTransactionId = lineage.ParentTransactionID().String()
	}

	// Convert child transaction IDs
	childIDs := lineage.ChildTransactionIDs()
	proto.ChildTransactionIds = make([]string, 0, len(childIDs))
	for _, id := range childIDs {
		proto.ChildTransactionIds = append(proto.ChildTransactionIds, id.String())
	}

	// Convert related transaction IDs
	relatedIDs := lineage.RelatedTransactionIDs()
	proto.RelatedTransactionIds = make([]string, 0, len(relatedIDs))
	for _, id := range relatedIDs {
		proto.RelatedTransactionIds = append(proto.RelatedTransactionIds, id.String())
	}

	return proto
}

// toProtoAuditTrailEntry converts a domain AuditTrailEntry to protobuf.
func toProtoAuditTrailEntry(entry *domain.AuditTrailEntry) *positionkeepingv1.AuditTrailEntry {
	if entry == nil {
		return nil
	}

	proto := &positionkeepingv1.AuditTrailEntry{
		AuditId:       entry.AuditID.String(),
		Timestamp:     timestamppb.New(entry.Timestamp),
		UserId:        entry.UserID,
		Action:        entry.Action,
		Details:       entry.Details,
		IpAddress:     entry.IPAddress,
		SystemContext: make(map[string]string),
	}

	// Copy system context
	for k, v := range entry.SystemContext {
		proto.SystemContext[k] = v
	}

	return proto
}

// toProtoStatusTracking converts a domain StatusTracking to protobuf.
func toProtoStatusTracking(tracking *domain.StatusTracking) *positionkeepingv1.StatusTracking {
	if tracking == nil {
		return nil
	}

	proto := &positionkeepingv1.StatusTracking{
		CurrentStatus:   toProtoTransactionStatus(tracking.CurrentStatus),
		StatusUpdatedAt: timestamppb.New(tracking.StatusUpdatedAt),
		StatusReason:    tracking.StatusReason,
		FailureReason:   tracking.FailureReason,
	}

	// Set previous status if present
	if tracking.PreviousStatus != nil {
		proto.PreviousStatus = toProtoTransactionStatus(*tracking.PreviousStatus)
	}

	return proto
}

// toProtoTransactionStatus converts a domain TransactionStatus to protobuf.
func toProtoTransactionStatus(status domain.TransactionStatus) commonv1.TransactionStatus {
	switch status {
	case domain.TransactionStatusPending:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING
	case domain.TransactionStatusPosted:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED
	case domain.TransactionStatusFailed:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED
	case domain.TransactionStatusCancelled:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED
	case domain.TransactionStatusReversed:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED
	case domain.TransactionStatusReconciled:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED // Reconciled maps to Posted
	case domain.TransactionStatusRejected:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED // Rejected maps to Failed
	case domain.TransactionStatusAmended:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING // Amended maps to Pending
	default:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
	}
}

// toProtoAccountServiceDomain converts a domain string to the proto enum.
func toProtoAccountServiceDomain(domain string) commonv1.AccountServiceDomain {
	switch domain {
	case "CURRENT_ACCOUNT":
		return commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_CURRENT_ACCOUNT
	case "INTERNAL_ACCOUNT":
		return commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_INTERNAL_ACCOUNT
	default:
		return commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED
	}
}

// fromProtoTransactionStatus converts protobuf TransactionStatus to domain.
func fromProtoTransactionStatus(status commonv1.TransactionStatus) domain.TransactionStatus {
	switch status {
	case commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING:
		return domain.TransactionStatusPending
	case commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED:
		return domain.TransactionStatusPosted
	case commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED:
		return domain.TransactionStatusFailed
	case commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED:
		return domain.TransactionStatusCancelled
	case commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED:
		return domain.TransactionStatusReversed
	case commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED:
		return domain.TransactionStatusPending // Default unspecified to Pending
	default:
		return domain.TransactionStatusPending
	}
}

// parseUUID parses a string as a UUID, returning an error if invalid.
func parseUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, ErrEmptyUUID
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID format: %w", err)
	}
	return id, nil
}
