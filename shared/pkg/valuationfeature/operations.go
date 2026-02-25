package valuationfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AccountResolver bridges the shared CRUD logic to service-specific account lookups.
// Each service (Current Account, Internal Account) provides its own implementation.
type AccountResolver interface {
	// ResolveAccount looks up an account and returns its UUID and native instrument.
	// Returns an error if the account is not found.
	ResolveAccount(ctx context.Context, accountID string) (accountUUID uuid.UUID, nativeInstrument string, err error)
}

// CreateParams holds inputs for creating a valuation feature.
type CreateParams struct {
	AccountID              string
	InstrumentCode         string
	ValuationMethodID      string
	ValuationMethodVersion int
	OutputInstrument       string // For native instrument validation
	Parameters             string // JSON string
	CreatedBy              string
}

// Create validates and persists a new ValuationFeature.
// It resolves the account, validates output_instrument matches native, creates, activates, and persists.
func Create(ctx context.Context, repo *Repository, resolver AccountResolver, params CreateParams) (*ValuationFeature, error) {
	// Resolve account → UUID + native instrument
	accountUUID, nativeInstrument, err := resolver.ResolveAccount(ctx, params.AccountID)
	if err != nil {
		return nil, fmt.Errorf("resolve account: %w", err)
	}

	// Validate output_instrument matches account's native instrument
	if params.OutputInstrument != nativeInstrument {
		return nil, fmt.Errorf(
			"%w: expected %s (account native instrument), got %s",
			ErrMethodOutputMismatch, nativeInstrument, params.OutputInstrument,
		)
	}

	// Parse method ID
	methodID, err := uuid.Parse(params.ValuationMethodID)
	if err != nil {
		return nil, fmt.Errorf("invalid valuation_method_id: %w", err)
	}

	// Parse parameters JSON if provided
	var parameters map[string]interface{}
	if params.Parameters != "" {
		if err := json.Unmarshal([]byte(params.Parameters), &parameters); err != nil {
			return nil, fmt.Errorf("invalid parameters JSON: %w", err)
		}
	}

	// Create the domain entity
	feature, err := NewValuationFeature(
		accountUUID,
		params.InstrumentCode,
		methodID,
		params.ValuationMethodVersion,
		parameters,
		params.CreatedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("create valuation feature: %w", err)
	}

	// Activate the feature immediately (standard flow)
	if err := feature.Activate(params.CreatedBy); err != nil {
		return nil, fmt.Errorf("activate valuation feature: %w", err)
	}

	// Save to database
	if err := repo.Create(ctx, feature); err != nil {
		return nil, fmt.Errorf("save valuation feature: %w", err)
	}

	return feature, nil
}

// UpdateAction specifies a lifecycle action for UpdateParams.
type UpdateAction int

const (
	// ActionActivate transitions from INITIATED to ACTIVE.
	ActionActivate UpdateAction = iota + 1
	// ActionTerminate transitions from ACTIVE to TERMINATED.
	ActionTerminate
)

// UpdateParams holds inputs for updating a valuation feature lifecycle.
type UpdateParams struct {
	FeatureID string
	Action    UpdateAction
	UpdatedBy string
}

// Update performs a lifecycle transition on a valuation feature.
func Update(ctx context.Context, repo *Repository, params UpdateParams) (*ValuationFeature, error) {
	featureID, err := uuid.Parse(params.FeatureID)
	if err != nil {
		return nil, fmt.Errorf("invalid feature_id: %w", err)
	}

	feature, err := repo.FindByID(ctx, featureID)
	if err != nil {
		return nil, fmt.Errorf("find valuation feature: %w", err)
	}

	switch params.Action {
	case ActionActivate:
		if err := feature.Activate(params.UpdatedBy); err != nil {
			return nil, fmt.Errorf("activate valuation feature: %w", err)
		}
	case ActionTerminate:
		if err := feature.Terminate(params.UpdatedBy); err != nil {
			return nil, fmt.Errorf("terminate valuation feature: %w", err)
		}
	default:
		return nil, ErrInvalidAction
	}

	if err := repo.Update(ctx, feature); err != nil {
		return nil, fmt.Errorf("update valuation feature: %w", err)
	}

	return feature, nil
}

// GetByID retrieves a valuation feature by its ID.
func GetByID(ctx context.Context, repo *Repository, featureID string) (*ValuationFeature, error) {
	id, err := uuid.Parse(featureID)
	if err != nil {
		return nil, fmt.Errorf("invalid feature_id: %w", err)
	}

	feature, err := repo.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("find valuation feature: %w", err)
	}

	return feature, nil
}

// GetByAccountAndInstrument retrieves a valuation feature by account, instrument, and knowledge time.
func GetByAccountAndInstrument(
	ctx context.Context,
	repo *Repository,
	resolver AccountResolver,
	accountID string,
	instrumentCode string,
	knowledgeAt *time.Time,
) (*ValuationFeature, error) {
	accountUUID, _, err := resolver.ResolveAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("resolve account: %w", err)
	}

	kt := time.Now()
	if knowledgeAt != nil {
		kt = *knowledgeAt
	}

	feature, err := repo.FindByAccountIDAndInstrument(ctx, accountUUID, instrumentCode, kt)
	if err != nil {
		return nil, fmt.Errorf("find valuation feature: %w", err)
	}

	return feature, nil
}

// ListParams holds inputs for listing valuation features.
type ListParams struct {
	AccountID       string
	LifecycleStatus *LifecycleStatus
}

// List retrieves all valuation features for an account.
func List(ctx context.Context, repo *Repository, resolver AccountResolver, params ListParams) ([]*ValuationFeature, error) {
	accountUUID, _, err := resolver.ResolveAccount(ctx, params.AccountID)
	if err != nil {
		return nil, fmt.Errorf("resolve account: %w", err)
	}

	features, err := repo.FindByAccountID(ctx, accountUUID, params.LifecycleStatus)
	if err != nil {
		return nil, fmt.Errorf("list valuation features: %w", err)
	}

	return features, nil
}
