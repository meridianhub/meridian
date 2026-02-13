package worker

import (
	"context"
	"time"
)

// SettlementSchedule represents a schedule record from Reference Data.
type SettlementSchedule struct {
	// ScheduleID uniquely identifies this schedule.
	ScheduleID string
	// AssetType is the instrument/asset type this schedule applies to.
	AssetType string
	// AccountID is the account to reconcile.
	AccountID string
	// CronExpression is the cron schedule (e.g., "0 2 * * *" for 2 AM daily).
	CronExpression string
	// SettlementType is the type of settlement (DAILY, WEEKLY, etc.).
	SettlementType string
	// Scope is the reconciliation scope (ACCOUNT, INSTRUMENT, etc.).
	Scope string
	// PeriodOffset is how far back from current time the period starts.
	PeriodOffset time.Duration
}

// ReferenceDataClient provides access to settlement schedule configuration.
type ReferenceDataClient interface {
	// ListSettlementSchedules retrieves all active settlement schedules.
	ListSettlementSchedules(ctx context.Context) ([]SettlementSchedule, error)
}

// ReconciliationClient initiates reconciliation runs via gRPC.
type ReconciliationClient interface {
	// InitiateReconciliation creates and starts a new settlement run.
	// Returns the run ID on success. Returns an error wrapping ErrRunAlreadyExists
	// if a run already exists for this account/period combination.
	InitiateReconciliation(ctx context.Context, req InitiateRequest) (string, error)
}

// InitiateRequest contains the parameters for initiating a reconciliation run.
type InitiateRequest struct {
	AccountID      string
	Scope          string
	SettlementType string
	PeriodStart    time.Time
	PeriodEnd      time.Time
	InitiatedBy    string
}
