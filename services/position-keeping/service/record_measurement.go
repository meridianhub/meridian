package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
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
	if err := validateRecordMeasurementRequest(req); err != nil {
		return nil, err
	}

	positionStateID, err := parseUUID(req.GetPositionStateId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid position_state_id: %v", err)
	}

	idempotencyKey, cachedResponse, err := s.checkMeasurementIdempotencyAndAcquireLock(ctx, req, positionStateID)
	if err != nil {
		return nil, err
	}
	if cachedResponse != nil {
		return cachedResponse, nil
	}

	if idempotencyKey != nil {
		defer func() {
			if err != nil {
				_ = s.idempotency.Delete(ctx, *idempotencyKey)
			}
		}()
	}

	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

	positionLog, err := s.loadPositionLog(ctx, positionStateID)
	if err != nil {
		return nil, err
	}

	measurement, err := s.buildAndValidateMeasurement(ctx, req, positionLog)
	if err != nil {
		return nil, err
	}

	if err := s.persistMeasurement(ctx, measurement); err != nil {
		return nil, err
	}

	if idempotencyKey != nil {
		if err := storeMeasurementIdempotencyResult(ctx, s.idempotency, *idempotencyKey, measurement.ID, positionStateID); err != nil {
			return nil, err
		}
	}

	return &positionkeepingv1.RecordMeasurementResponse{
		MeasurementId:   measurement.ID.String(),
		PositionStateId: positionStateID.String(),
		RecordedAt:      timestamppb.New(measurement.CreatedAt),
	}, nil
}

// loadPositionLog retrieves a position log, returning gRPC status errors.
func (s *PositionKeepingService) loadPositionLog(ctx context.Context, positionStateID uuid.UUID) (*domain.FinancialPositionLog, error) {
	positionLog, err := s.repository.FindByID(ctx, positionStateID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "position state not found: %s", positionStateID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve position state: %v", err)
	}

	// In multi-tenant mode, the repository already scopes queries by tenant schema.
	if tenantID, ok := tenant.FromContext(ctx); ok {
		_ = tenantID
	}

	return positionLog, nil
}

// buildAndValidateMeasurement parses request fields, runs CEL validation, and creates the domain measurement.
func (s *PositionKeepingService) buildAndValidateMeasurement(
	ctx context.Context,
	req *positionkeepingv1.RecordMeasurementRequest,
	positionLog *domain.FinancialPositionLog,
) (*domain.Measurement, error) {
	measurementValue, err := decimal.NewFromString(req.GetValue())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid measurement value: %v", err)
	}

	metadata := make(map[string]string)
	for k, v := range req.GetMetadata() {
		metadata[k] = v
	}

	instrumentCode := req.GetMeasurementType()
	validationResult, err := s.validateMeasurementWithCEL(ctx, instrumentCode, req.GetValue(), metadata, positionLog.AccountID)
	if err != nil {
		return nil, err
	}

	userID := audit.GetUserFromContext(ctx)
	measurement, err := domain.NewMeasurement(
		positionLog.LogID,
		domain.ParseMeasurementType(req.GetMeasurementType()),
		measurementValue,
		req.GetUnit(),
		req.GetTimestamp().AsTime(),
		metadata,
		validationResult.BucketID,
		userID,
	)
	if err != nil {
		return nil, mapMeasurementCreationError(err)
	}

	return measurement, nil
}

// mapMeasurementCreationError maps domain measurement errors to gRPC status errors.
func mapMeasurementCreationError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNegativeMeasurementValue):
		return status.Error(codes.InvalidArgument, "measurement value must be positive")
	case errors.Is(err, domain.ErrFutureTimestamp):
		return status.Error(codes.InvalidArgument, "measurement timestamp cannot be in the future")
	case errors.Is(err, domain.ErrInvalidMeasurementType):
		return status.Error(codes.InvalidArgument, "invalid measurement type")
	case errors.Is(err, domain.ErrInvalidUnit):
		return status.Error(codes.InvalidArgument, "measurement unit is required")
	default:
		return status.Errorf(codes.InvalidArgument, "failed to create measurement: %v", err)
	}
}

// persistMeasurement saves a measurement to the repository, mapping errors to gRPC status.
func (s *PositionKeepingService) persistMeasurement(ctx context.Context, measurement *domain.Measurement) error {
	if err := s.measurementRepo.Create(ctx, measurement); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return status.Error(codes.AlreadyExists, "measurement already exists")
		}
		if errors.Is(err, domain.ErrNotFound) {
			return status.Error(codes.NotFound, "position state not found")
		}
		return status.Errorf(codes.Internal, "failed to save measurement: %v", err)
	}
	return nil
}

// storeMeasurementIdempotencyResult stores the idempotency result for a measurement operation.
func storeMeasurementIdempotencyResult(ctx context.Context, svc idempotency.Service, key idempotency.Key, measurementID, positionStateID uuid.UUID) error {
	resultData, err := json.Marshal(map[string]string{
		"measurement_id":    measurementID.String(),
		"position_state_id": positionStateID.String(),
	})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal idempotency result: %v", err)
	}

	if err := svc.StoreResult(ctx, idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        resultData,
		CompletedAt: time.Now(),
		TTL:         24 * time.Hour,
	}); err != nil {
		return status.Errorf(codes.Internal, "failed to store idempotency result: %v", err)
	}
	return nil
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

// errUnexpectedAttributesType is returned when the CEL activation attributes value is not map[string]string.
var errUnexpectedAttributesType = errors.New("attributes activation value has unexpected type")

// MeasurementValidationResult contains the result of CEL validation and bucket key generation.
type MeasurementValidationResult struct {
	// BucketID is the generated bucket key for the measurement.
	// Empty string if no bucket key expression is defined for the instrument.
	BucketID string
}

// validateMeasurementWithCEL performs CEL-based validation of measurement attributes
// against the instrument definition and generates a bucket key if configured.
// This is optional - if instrumentCache is nil, validation is skipped for backwards compatibility.
//
// The CEL program receives the following variables:
//   - attributes: map[string]string of measurement metadata
//   - amount: string representation of the measurement value
//   - valid_from: zero time (for future use)
//   - valid_to: zero time (for future use)
//   - source: extracted from metadata["source"] or empty string
//
// Returns the validation result (including bucket ID) and nil error if validation passes.
// Returns nil result and gRPC INVALID_ARGUMENT error if validation fails.
// Returns nil result and gRPC NOT_FOUND error if instrument is not found.
// Returns nil result and gRPC RESOURCE_EXHAUSTED error if bucket cardinality limit exceeded.
func (s *PositionKeepingService) validateMeasurementWithCEL(
	ctx context.Context,
	instrumentCode string,
	amount string,
	metadata map[string]string,
	accountID string,
) (*MeasurementValidationResult, error) {
	// Skip validation if instrument cache is not configured (backwards compatibility)
	if s.instrumentCache == nil {
		return &MeasurementValidationResult{}, nil
	}

	instrument, err := s.loadInstrument(ctx, instrumentCode)
	if err != nil {
		RecordValidationFailure(instrumentCode, ValidationFailureReasonInstrumentNotFound)
		return nil, status.Errorf(codes.NotFound,
			"instrument definition not found for measurement type '%s': %v", instrumentCode, err)
	}

	activation := buildCELActivation(metadata, amount)

	if err := evalValidationProgram(instrument, instrumentCode, activation, func(code string, reason string) {
		RecordValidationFailure(code, reason)
	}); err != nil {
		return nil, err
	}

	attrs, ok := activation["attributes"].(map[string]string)
	if !ok {
		return nil, errUnexpectedAttributesType
	}
	bucketID, err := s.evalBucketKeyProgram(ctx, instrument, instrumentCode, attrs, accountID)
	if err != nil {
		return nil, err
	}

	return &MeasurementValidationResult{
		BucketID: bucketID,
	}, nil
}

// evalBucketKeyProgram generates a bucket key using the instrument's CEL program and checks cardinality.
func (s *PositionKeepingService) evalBucketKeyProgram(
	ctx context.Context,
	instrument *CachedInstrument,
	instrumentCode string,
	attributesMap map[string]string,
	accountID string,
) (string, error) {
	if instrument.BucketKeyProgram == nil {
		return "", nil
	}

	bucketActivation := map[string]any{
		"attributes": attributesMap,
	}

	result, _, err := instrument.BucketKeyProgram.Eval(bucketActivation)
	if err != nil {
		RecordValidationFailure(instrumentCode, ValidationFailureReasonBucketKeyError)
		return "", status.Errorf(codes.InvalidArgument,
			"bucket key generation error for measurement type '%s': %v", instrumentCode, err)
	}

	key, ok := result.Value().(string)
	if !ok {
		RecordValidationFailure(instrumentCode, ValidationFailureReasonBucketKeyError)
		return "", status.Errorf(codes.InvalidArgument,
			"bucket key generation error for measurement type '%s': expression did not return string", instrumentCode)
	}

	if err := s.checkBucketCardinality(ctx, accountID, instrumentCode, key); err != nil {
		return "", err
	}

	return key, nil
}

// checkBucketCardinality checks whether adding a new bucket would exceed the cardinality limit.
func (s *PositionKeepingService) checkBucketCardinality(ctx context.Context, accountID, instrumentCode, bucketID string) error {
	if s.bucketCounter == nil || bucketID == "" {
		return nil
	}

	count, err := s.bucketCounter.CountBuckets(ctx, accountID, instrumentCode)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to check bucket cardinality for account '%s' instrument '%s': %v",
			accountID, instrumentCode, err)
	}

	if count >= MaxBucketsPerAccountInstrument {
		RecordCardinalityViolation(instrumentCode)
		return status.Errorf(codes.ResourceExhausted,
			"bucket cardinality limit exceeded for account/instrument: %d buckets (limit: %d)",
			count, MaxBucketsPerAccountInstrument)
	}

	return nil
}
