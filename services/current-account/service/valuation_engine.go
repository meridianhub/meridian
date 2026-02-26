// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ValuationEngine defines the interface for executing valuations.
// This abstraction allows the Current Account service to invoke valuation logic
// without coupling to the implementation details of the valuation method execution.
type ValuationEngine interface {
	// Evaluate executes a valuation method with the given parameters.
	// Returns the output amount and analysis, or an error if execution fails.
	Evaluate(ctx context.Context, params ValuationParams) (*ValuationResult, error)
}

// ValuationParams contains the parameters for a valuation execution.
type ValuationParams struct {
	MethodID      uuid.UUID
	MethodVersion int
	InputAmount   decimal.Decimal
	InputCode     string
	OutputCode    string
	Parameters    map[string]interface{}
	KnowledgeAt   time.Time
}

// ValuationResult contains the result of a valuation execution.
type ValuationResult struct {
	OutputAmount    decimal.Decimal
	OutputCode      string
	AppliedRates    map[string]string
	ObservationIDs  []string
	ComputedAt      time.Time
	CalculationPath []string
	DegradedMode    bool
	CacheHit        bool
	Warnings        []ValuationWarningResult
	MarketQualities []MarketDataQualityResult
}

// ValuationWarningResult holds warning information from valuation execution.
type ValuationWarningResult struct {
	Code     string
	Message  string
	Severity string
}

// MarketDataQualityResult holds market data quality information.
type MarketDataQualityResult struct {
	Source           string
	QualityLevel     string
	ObservedAt       time.Time
	StalenessSeconds int64
}

// Valuation-specific sentinel errors for valuateInternal.
var (
	ErrValuationRepoNotConfigured = errors.New("valuation feature repository not configured")
	ErrValuationAccountNotFound   = errors.New("account not found")
	ErrNoActiveValuationFeature   = errors.New("no active valuation feature")
	ErrValuationFeatureNotActive  = errors.New("valuation feature not active")
	ErrValuationEngineFailed      = errors.New("valuation engine failed")
)

// Valuation operation status constants for metrics
const (
	opStatusValuationEngineNil     = "valuation_engine_nil"
	opStatusNoValuationFeature     = "no_valuation_feature"
	opStatusValuationFailed        = "valuation_failed"
	opStatusInvalidInputAmount     = "invalid_input_amount"
	opStatusFeatureNotActive       = "feature_not_active"
	opStatusInputInstrumentEmpty   = "input_instrument_empty"
	opStatusInputAmountNonPositive = "input_amount_non_positive"
)

// valuateInternalResult contains the result of an internal valuation operation.
// This is the shared result type used by both EvaluateAssetValuation (inquiry)
// and InitiateLien (binding) to guarantee identical pricing logic.
type valuateInternalResult struct {
	OutputAmount decimal.Decimal
	OutputCode   string
	Analysis     *pb.ValuationAnalysis
	CacheHit     bool
	ExecutionMs  int64
}

// valuateInternal is the SINGLE valuation function shared by both:
//   - EvaluateAssetValuation (inquiry-only, projectedBalance=nil)
//   - InitiateLien (binding, projectedBalance=actual balance)
//
// CRITICAL: This function MUST be used for ALL valuation operations to prevent Ghost Pricing.
// Ghost Pricing occurs when an inquiry shows one price but the binding operation uses a different price.
// By routing both through this function, we guarantee identical results for identical inputs.
func (s *Service) valuateInternal(ctx context.Context, accountID string, inputAmount decimal.Decimal, inputCode string, knowledgeAt time.Time) (*valuateInternalResult, error) {
	start := time.Now()

	// Validate valuation feature repository is configured
	if s.valuationFeatureRepo == nil {
		return nil, ErrValuationRepoNotConfigured
	}

	// Retrieve account to get native instrument
	account, err := s.repo.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrValuationAccountNotFound, accountID)
		}
		return nil, fmt.Errorf("failed to retrieve account: %w", err)
	}

	nativeInstrument := account.Balance().InstrumentCode()

	// If input instrument matches native instrument, no conversion needed
	if inputCode == nativeInstrument {
		executionMs := time.Since(start).Milliseconds()
		return &valuateInternalResult{
			OutputAmount: inputAmount,
			OutputCode:   nativeInstrument,
			Analysis: &pb.ValuationAnalysis{
				MethodId:        "identity",
				MethodVersion:   "1",
				AppliedRates:    map[string]string{"identity_rate": "1.0"},
				ComputedAt:      timestamppb.New(time.Now()),
				KnowledgeAt:     timestamppb.New(knowledgeAt),
				CalculationPath: []string{"identity_conversion"},
				DegradedMode:    false,
			},
			CacheHit:    false,
			ExecutionMs: executionMs,
		}, nil
	}

	// Resolve the active valuation feature for this account+instrument at knowledgeAt
	feature, err := s.valuationFeatureRepo.FindByAccountIDAndInstrument(ctx, account.ID(), inputCode, knowledgeAt)
	if err != nil {
		if errors.Is(err, persistence.ErrValuationFeatureNotFound) {
			return nil, fmt.Errorf("%w for account %s and instrument %s", ErrNoActiveValuationFeature, accountID, inputCode)
		}
		return nil, fmt.Errorf("failed to resolve valuation feature: %w", err)
	}

	// Verify feature is active
	if !feature.IsActive() {
		return nil, fmt.Errorf("%w: %s (status: %s)", ErrValuationFeatureNotActive, feature.ID, feature.LifecycleStatus)
	}

	// Convert parameters to proto Struct for analysis
	var accountParams *structpb.Struct
	if feature.Parameters != nil {
		accountParams, err = structpb.NewStruct(feature.Parameters)
		if err != nil {
			s.logger.Warn("failed to convert valuation feature parameters to proto struct",
				"account_id", accountID,
				"feature_id", feature.ID,
				"error", err)
			// Continue without account parameters rather than failing the valuation
		}
	}

	// If a valuation engine is configured, delegate to it
	if s.valuationEngine != nil {
		result, err := s.valuationEngine.Evaluate(ctx, ValuationParams{
			MethodID:      feature.ValuationMethodID,
			MethodVersion: feature.ValuationMethodVersion,
			InputAmount:   inputAmount,
			InputCode:     inputCode,
			OutputCode:    nativeInstrument,
			Parameters:    feature.Parameters,
			KnowledgeAt:   knowledgeAt,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrValuationEngineFailed, err)
		}

		executionMs := time.Since(start).Milliseconds()

		// Build analysis from engine result
		analysis := &pb.ValuationAnalysis{
			MethodId:          feature.ValuationMethodID.String(),
			MethodVersion:     strconv.Itoa(feature.ValuationMethodVersion),
			AppliedRates:      result.AppliedRates,
			ObservationIds:    result.ObservationIDs,
			ComputedAt:        timestamppb.New(result.ComputedAt),
			KnowledgeAt:       timestamppb.New(knowledgeAt),
			AccountParameters: accountParams,
			CalculationPath:   result.CalculationPath,
			DegradedMode:      result.DegradedMode,
		}

		// Convert warnings
		for _, w := range result.Warnings {
			analysis.Warnings = append(analysis.Warnings, &pb.ValuationWarning{
				Code:     w.Code,
				Message:  w.Message,
				Severity: w.Severity,
			})
		}

		// Convert market data qualities
		for _, q := range result.MarketQualities {
			analysis.MarketDataQualities = append(analysis.MarketDataQualities, &pb.MarketDataQuality{
				Source:           q.Source,
				QualityLevel:     q.QualityLevel,
				ObservedAt:       timestamppb.New(q.ObservedAt),
				StalenessSeconds: q.StalenessSeconds,
			})
		}

		return &valuateInternalResult{
			OutputAmount: result.OutputAmount,
			OutputCode:   result.OutputCode,
			Analysis:     analysis,
			CacheHit:     result.CacheHit,
			ExecutionMs:  executionMs,
		}, nil
	}

	// Fallback: No valuation engine configured.
	// Use the feature's method reference to build a stub analysis.
	// In production, the valuation engine MUST be configured.
	executionMs := time.Since(start).Milliseconds()

	s.logger.Warn("no valuation engine configured, returning unvalued amount with degraded analysis",
		"account_id", accountID,
		"input_code", inputCode,
		"method_id", feature.ValuationMethodID.String())

	return &valuateInternalResult{
		OutputAmount: inputAmount, // Passthrough (no actual conversion)
		OutputCode:   nativeInstrument,
		Analysis: &pb.ValuationAnalysis{
			MethodId:          feature.ValuationMethodID.String(),
			MethodVersion:     strconv.Itoa(feature.ValuationMethodVersion),
			AppliedRates:      map[string]string{"passthrough": "1.0"},
			ComputedAt:        timestamppb.New(time.Now()),
			KnowledgeAt:       timestamppb.New(knowledgeAt),
			AccountParameters: accountParams,
			CalculationPath:   []string{"passthrough_no_engine"},
			DegradedMode:      true,
			Warnings: []*pb.ValuationWarning{
				{
					Code:     "NO_VALUATION_ENGINE",
					Message:  "Valuation engine not configured; returning passthrough amount",
					Severity: "DEGRADED",
				},
			},
		},
		CacheHit:    false,
		ExecutionMs: executionMs,
	}, nil
}

// EvaluateAssetValuation performs an inquiry-only (non-binding) valuation.
// Returns the estimated valued amount without creating any financial commitment.
// CRITICAL: Uses valuateInternal() which is shared with InitiateLien to prevent Ghost Pricing.
func (s *Service) EvaluateAssetValuation(ctx context.Context, req *pb.EvaluateAssetValuationRequest) (*pb.EvaluateAssetValuationResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("evaluate_asset_valuation", operationStatus, time.Since(start))
	}()

	// Validate valuation feature repository is configured
	if s.valuationFeatureRepo == nil {
		operationStatus = opStatusValuationFeatureRepoNil
		return nil, status.Error(codes.FailedPrecondition, "valuation feature operations not configured")
	}

	// Validate input
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Input == nil {
		operationStatus = opStatusInvalidRequest
		return nil, status.Error(codes.InvalidArgument, "input amount is required")
	}
	if req.Input.InstrumentCode == "" {
		operationStatus = opStatusInputInstrumentEmpty
		return nil, status.Error(codes.InvalidArgument, "input instrument_code is required")
	}
	if req.Input.Amount == "" {
		operationStatus = opStatusInvalidInputAmount
		return nil, status.Error(codes.InvalidArgument, "input amount value is required")
	}

	// Parse input amount
	inputAmount, err := decimal.NewFromString(req.Input.Amount)
	if err != nil {
		operationStatus = opStatusInvalidInputAmount
		return nil, status.Errorf(codes.InvalidArgument, "invalid input amount: %v", err)
	}

	if !inputAmount.IsPositive() {
		operationStatus = opStatusInputAmountNonPositive
		return nil, status.Error(codes.InvalidArgument, "input amount must be positive")
	}

	// Determine knowledge_at time
	knowledgeAt := time.Now()
	if req.KnowledgeAt != nil {
		knowledgeAt = req.KnowledgeAt.AsTime()
	}

	// Execute valuation via shared function (prevents Ghost Pricing)
	result, err := s.valuateInternal(ctx, req.AccountId, inputAmount, req.Input.InstrumentCode, knowledgeAt)
	if err != nil {
		// Map internal errors to gRPC status codes using sentinel errors
		switch {
		case errors.Is(err, ErrValuationAccountNotFound):
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "%v", err)
		case errors.Is(err, ErrNoActiveValuationFeature):
			operationStatus = opStatusNoValuationFeature
			return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
		case errors.Is(err, ErrValuationFeatureNotActive):
			operationStatus = opStatusFeatureNotActive
			return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
		case errors.Is(err, ErrValuationRepoNotConfigured):
			operationStatus = opStatusValuationFeatureRepoNil
			return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
		case errors.Is(err, ErrValuationEngineFailed):
			operationStatus = opStatusValuationFailed
			return nil, status.Errorf(codes.Internal, "%v", err)
		default:
			operationStatus = opStatusValuationFailed
			return nil, status.Errorf(codes.Internal, "valuation failed: %v", err)
		}
	}

	executionMs := time.Since(start).Milliseconds()

	return &pb.EvaluateAssetValuationResponse{
		Output: &quantityv1.InstrumentAmount{
			Amount:         result.OutputAmount.String(),
			InstrumentCode: result.OutputCode,
			Version:        1,
		},
		Basis:           result.Analysis,
		ExecutionTimeMs: strconv.FormatInt(executionMs, 10),
		CacheHit:        result.CacheHit,
		IsEstimate:      true, // Always true for inquiry operations
	}, nil
}
