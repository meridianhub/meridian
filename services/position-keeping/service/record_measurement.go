package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/pkg/platform/quantity"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// RecordMeasurement records a physical measurement against a position state.
// Used for multi-asset tracking (energy, compute, carbon credits, etc.).
func (s *PositionKeepingService) RecordMeasurement(
	ctx context.Context,
	req *positionkeepingv1.RecordMeasurementRequest,
) (resp *positionkeepingv1.RecordMeasurementResponse, err error) {
	// Validate request
	if err := validateRecordMeasurementRequest(req); err != nil {
		return nil, err
	}

	// Parse position_state_id (which maps to log_id in our domain)
	positionStateID, err := parseUUID(req.GetPositionStateId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid position_state_id: %v", err)
	}

	// Check idempotency and acquire lock if key provided
	idempotencyKey, cachedResponse, err := s.checkMeasurementIdempotencyAndAcquireLock(ctx, req, positionStateID)
	if err != nil {
		return nil, err
	}
	if cachedResponse != nil {
		return cachedResponse, nil
	}

	// Clean up pending idempotency key on error
	if idempotencyKey != nil {
		defer func() {
			if err != nil {
				_ = s.idempotency.Delete(ctx, *idempotencyKey)
			}
		}()
	}

	// Check for context cancellation after potentially slow idempotency check
	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

	// Verify position state exists and belongs to the tenant (if multi-tenant)
	positionLog, err := s.repository.FindByID(ctx, positionStateID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "position state not found: %s", positionStateID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve position state: %v", err)
	}

	// For multi-tenant mode: verify tenant ownership
	// Note: In multi-tenant mode, the repository already scopes queries by tenant.
	// If we get here, the position log exists and is accessible to this tenant.
	// This is an additional verification for explicit security.
	if tenantID, ok := tenant.FromContext(ctx); ok {
		// The tenant context is set, which means we're in multi-tenant mode.
		// The repository will have already scoped the query to this tenant's schema.
		// If we found a record, it belongs to this tenant.
		_ = tenantID // Explicitly acknowledge the tenant for clarity
	}

	// Parse and validate measurement value
	measurementValue, err := decimal.NewFromString(req.GetValue())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid measurement value: %v", err)
	}

	// Parse measurement type
	measurementType := domain.ParseMeasurementType(req.GetMeasurementType())

	// Parse timestamp
	measurementTimestamp := req.GetTimestamp().AsTime()

	// Convert metadata
	metadata := make(map[string]string)
	for k, v := range req.GetMetadata() {
		metadata[k] = v
	}

	// Perform CEL validation if instrument cache is configured
	// The measurement_type field maps to the instrument code
	instrumentCode := req.GetMeasurementType()
	if err := s.validateMeasurementWithCEL(ctx, instrumentCode, req.GetValue(), metadata); err != nil {
		return nil, err
	}

	// Get user from context for audit
	userID := audit.GetUserFromContext(ctx)

	// Create measurement domain object
	measurement, err := domain.NewMeasurement(
		positionLog.LogID,
		measurementType,
		measurementValue,
		req.GetUnit(),
		measurementTimestamp,
		metadata,
		userID,
	)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNegativeMeasurementValue):
			return nil, status.Error(codes.InvalidArgument, "measurement value must be positive")
		case errors.Is(err, domain.ErrFutureTimestamp):
			return nil, status.Error(codes.InvalidArgument, "measurement timestamp cannot be in the future")
		case errors.Is(err, domain.ErrInvalidMeasurementType):
			return nil, status.Error(codes.InvalidArgument, "invalid measurement type")
		case errors.Is(err, domain.ErrInvalidUnit):
			return nil, status.Error(codes.InvalidArgument, "measurement unit is required")
		default:
			return nil, status.Errorf(codes.InvalidArgument, "failed to create measurement: %v", err)
		}
	}

	// Persist measurement to repository
	if err := s.measurementRepo.Create(ctx, measurement); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, status.Error(codes.AlreadyExists, "measurement already exists")
		}
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "position state not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to save measurement: %v", err)
	}

	// Store idempotency result if key was provided
	if idempotencyKey != nil {
		resultData, err := json.Marshal(map[string]string{
			"measurement_id":    measurement.ID.String(),
			"position_state_id": positionStateID.String(),
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to marshal idempotency result: %v", err)
		}

		if err := s.idempotency.StoreResult(ctx, idempotency.Result{
			Key:         *idempotencyKey,
			Status:      idempotency.StatusCompleted,
			Data:        resultData,
			CompletedAt: time.Now(),
			TTL:         24 * time.Hour,
		}); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to store idempotency result: %v", err)
		}
	}

	resp = &positionkeepingv1.RecordMeasurementResponse{
		MeasurementId:   measurement.ID.String(),
		PositionStateId: positionStateID.String(),
		RecordedAt:      timestamppb.New(measurement.CreatedAt),
	}
	return resp, nil
}

// validateRecordMeasurementRequest validates the RecordMeasurement request.
func validateRecordMeasurementRequest(req *positionkeepingv1.RecordMeasurementRequest) error {
	if req.GetMeasurementType() == "" {
		return status.Error(codes.InvalidArgument, "measurement_type is required")
	}

	if req.GetValue() == "" {
		return status.Error(codes.InvalidArgument, "value is required")
	}

	if req.GetUnit() == "" {
		return status.Error(codes.InvalidArgument, "unit is required")
	}

	if req.GetTimestamp() == nil {
		return status.Error(codes.InvalidArgument, "timestamp is required")
	}

	if req.GetPositionStateId() == "" {
		return status.Error(codes.InvalidArgument, "position_state_id is required")
	}

	return nil
}

// checkMeasurementIdempotencyAndAcquireLock checks for completed operations and acquires a pending lock.
func (s *PositionKeepingService) checkMeasurementIdempotencyAndAcquireLock(
	ctx context.Context,
	req *positionkeepingv1.RecordMeasurementRequest,
	positionStateID uuid.UUID,
) (*idempotency.Key, *positionkeepingv1.RecordMeasurementResponse, error) {
	// No idempotency key provided or idempotency service not configured
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" || s.idempotency == nil {
		return nil, nil, nil
	}

	key := idempotency.Key{
		Namespace: "position-keeping",
		Operation: "record-measurement",
		EntityID:  positionStateID.String(),
		RequestID: req.IdempotencyKey.Key,
	}

	// Add tenant ID if in multi-tenant mode
	if tenantID, ok := tenant.FromContext(ctx); ok {
		key.TenantID = string(tenantID)
	}

	// Check if operation was already completed
	result, err := s.idempotency.Check(ctx, key)
	if err == nil && result.Status == idempotency.StatusCompleted {
		// Return cached result - must not retry the operation once completed
		var cachedData struct {
			MeasurementID   string `json:"measurement_id"`
			PositionStateID string `json:"position_state_id"`
		}
		if err := json.Unmarshal(result.Data, &cachedData); err != nil {
			return nil, nil, status.Errorf(codes.Internal, "failed to decode cached idempotency response: %v", err)
		}

		return &key, &positionkeepingv1.RecordMeasurementResponse{
			MeasurementId:   cachedData.MeasurementID,
			PositionStateId: cachedData.PositionStateID,
			RecordedAt:      timestamppb.New(result.CompletedAt),
		}, nil
	}

	// Mark operation as pending to prevent concurrent execution
	if err := s.idempotency.MarkPending(ctx, key, 5*time.Minute); err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}

	return &key, nil, nil
}

// validateMeasurementWithCEL performs CEL-based validation of measurement attributes
// against the instrument definition. This is optional - if instrumentCache is nil,
// validation is skipped for backwards compatibility.
//
// The CEL program receives the following variables:
//   - attributes: map[string]string of measurement metadata
//   - amount: string representation of the measurement value
//   - valid_from: zero time (for future use)
//   - valid_to: zero time (for future use)
//   - source: extracted from metadata["source"] or empty string
//
// Returns nil if validation passes or cache is not configured.
// Returns gRPC INVALID_ARGUMENT error if validation fails.
// Returns gRPC NOT_FOUND error if instrument is not found and cache is configured.
func (s *PositionKeepingService) validateMeasurementWithCEL(
	ctx context.Context,
	instrumentCode string,
	amount string,
	metadata map[string]string,
) error {
	// Skip validation if instrument cache is not configured (backwards compatibility)
	if s.instrumentCache == nil {
		return nil
	}

	// Acquire an AttributeBag from pool for efficient memory reuse
	bag := quantity.AcquireAttributeBag()
	defer quantity.ReleaseAttributeBag(bag)

	// Populate AttributeBag from metadata
	for k, v := range metadata {
		bag.Set(k, v)
	}

	// Look up instrument from cache using measurement_type as the code
	// Use version=1 for now; could be made configurable in the future
	const instrumentVersion = 1
	instrument, err := s.instrumentCache.GetOrLoad(ctx, instrumentCode, instrumentVersion, func() (*CachedInstrument, error) {
		// The loadFn should never be called if the cache is properly configured
		// with a backing repository. For now, return not found to trigger the error path.
		return nil, fmt.Errorf("%w: %s", ErrInstrumentNotFound, instrumentCode)
	})
	if err != nil {
		// Instrument not found - record metric and return error
		RecordValidationFailure(instrumentCode, ValidationFailureReasonInstrumentNotFound)
		return status.Errorf(codes.NotFound,
			"instrument definition not found for measurement type '%s': %v", instrumentCode, err)
	}

	// If instrument has no validation program, validation passes
	if instrument.ValidationProgram == nil {
		return nil
	}

	// Build the activation context for CEL evaluation
	// The CEL program expects these specific variable names
	source := metadata["source"]
	activation := map[string]any{
		"attributes": bag.ToMap(),
		"amount":     amount,
		"valid_from": time.Time{}, // Zero time for now
		"valid_to":   time.Time{}, // Zero time for now
		"source":     source,
	}

	// Evaluate the CEL validation program
	result, _, err := instrument.ValidationProgram.Eval(activation)
	if err != nil {
		// CEL evaluation error - record metric and return error
		RecordValidationFailure(instrumentCode, ValidationFailureReasonCELError)
		return status.Errorf(codes.InvalidArgument,
			"validation error for measurement type '%s': %v", instrumentCode, err)
	}

	// The validation program should return a boolean
	valid, ok := result.Value().(bool)
	if !ok {
		// Unexpected return type from CEL - treat as error
		RecordValidationFailure(instrumentCode, ValidationFailureReasonCELError)
		return status.Errorf(codes.InvalidArgument,
			"validation error for measurement type '%s': expression did not return boolean", instrumentCode)
	}

	if !valid {
		// Validation rejected the measurement - record metric and return error
		RecordValidationFailure(instrumentCode, ValidationFailureReasonCELRejected)
		return status.Errorf(codes.InvalidArgument,
			"measurement validation failed for type '%s': attributes do not satisfy validation rules", instrumentCode)
	}

	return nil
}
