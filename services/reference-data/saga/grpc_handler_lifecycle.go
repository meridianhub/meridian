package saga

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/validation"
)

// CreateSagaDraft creates a new saga definition in DRAFT status.
// If a DryRunValidator is configured, the script is validated before persistence.
// Invalid scripts are rejected with INVALID_ARGUMENT status.
func (h *RegistryHandler) CreateSagaDraft(
	ctx context.Context,
	req *sagav1.CreateSagaDraftRequest,
) (*sagav1.CreateSagaDraftResponse, error) {
	version := int(req.Version)
	if version < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "version must be non-negative")
	}
	if version == 0 {
		version = 1
	}

	def := &Definition{
		ID:                      uuid.New(),
		Name:                    req.Name,
		Version:                 version,
		Script:                  req.Script,
		Status:                  StatusDraft,
		IsSystem:                false,
		PreconditionsExpression: req.PreconditionsExpression,
		DisplayName:             req.DisplayName,
		Description:             req.Description,
	}

	// Mandatory dry-run validation: reject invalid scripts before persistence
	if h.dryRunValidator != nil && req.Script != "" {
		dryRunResult, err := h.dryRunValidator.Validate(ctx, req.Script)
		if err != nil {
			h.logger.Error("dry-run validation error during draft creation",
				"saga_name", req.Name,
				"error", err)
			return nil, status.Errorf(codes.Internal, "validation error: %v", err)
		}

		if !dryRunResult.Success {
			formatter := &validation.HumanReadableFormatter{}
			errMsg := formatter.Format(dryRunResult)

			h.logger.Warn("saga script validation failed",
				"saga_name", req.Name,
				"version", version,
				"error_count", len(dryRunResult.Errors))

			return nil, status.Errorf(codes.InvalidArgument,
				"saga script validation failed:\n%s", errMsg)
		}

		// Store validation metadata on the definition
		now := time.Now()
		complexityScore := calculateComplexityScore(dryRunResult.Metrics.HandlerCallCount)
		handlerCallCount := dryRunResult.Metrics.HandlerCallCount
		def.ValidationStatus = "PASSED"
		def.ComplexityScore = &complexityScore
		def.HandlerCallCount = &handlerCallCount
		def.ValidatedAt = &now
	}

	if err := h.registry.CreateDraft(ctx, def); err != nil {
		return nil, h.mapDomainError(err, "CreateSagaDraft", req.Name)
	}

	h.logger.Info("saga draft created",
		"name", def.Name,
		"version", def.Version,
		"id", def.ID)

	// Perform reference validation (non-blocking)
	var validationResult *sagav1.ValidationResult
	if h.validator != nil {
		result, err := h.validator.ValidateDraft(ctx, def.ID, def.Script)
		if err != nil {
			h.logger.Warn("reference validation failed during draft creation",
				"saga_id", def.ID,
				"error", err)
		} else {
			validationResult = h.validationResultToProto(result)
		}
	}

	// Re-fetch the saga to get timestamps set by the database
	created, err := h.registry.GetByID(ctx, def.ID)
	if err != nil {
		return nil, h.mapDomainError(err, "CreateSagaDraft", req.Name)
	}

	return &sagav1.CreateSagaDraftResponse{
		Saga:       h.definitionToProto(created),
		Validation: validationResult,
	}, nil
}

// UpdateSagaDefinition modifies a DRAFT saga definition.
// Only fields that are explicitly set in the request are updated.
func (h *RegistryHandler) UpdateSagaDefinition(
	ctx context.Context,
	req *sagav1.UpdateSagaDefinitionRequest,
) (*sagav1.UpdateSagaDefinitionResponse, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid saga id: %v", err)
	}

	// Only update fields that are explicitly provided (proto3 optional fields)
	updates := &Definition{}
	if req.Script != nil {
		updates.Script = *req.Script
	}
	if req.PreconditionsExpression != nil {
		updates.PreconditionsExpression = *req.PreconditionsExpression
	}
	if req.DisplayName != nil {
		updates.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		updates.Description = *req.Description
	}

	if err := h.registry.UpdateDefinition(ctx, id, updates); err != nil {
		return nil, h.mapDomainError(err, "UpdateSagaDefinition", id.String())
	}

	// Retrieve the updated definition
	updated, err := h.registry.GetByID(ctx, id)
	if err != nil {
		return nil, h.mapDomainError(err, "UpdateSagaDefinition", id.String())
	}

	h.logger.Info("saga definition updated",
		"id", id,
		"name", updated.Name)

	return &sagav1.UpdateSagaDefinitionResponse{
		Saga: h.definitionToProto(updated),
	}, nil
}

// ActivateSaga transitions a saga from DRAFT to ACTIVE.
func (h *RegistryHandler) ActivateSaga(
	ctx context.Context,
	req *sagav1.ActivateSagaRequest,
) (*sagav1.ActivateSagaResponse, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid saga id: %v", err)
	}

	// Perform activation validation if validator is configured
	var validationResult *sagav1.ValidationResult
	if h.validator != nil {
		saga, err := h.registry.GetByID(ctx, id)
		if err != nil {
			return nil, h.mapDomainError(err, "ActivateSaga", id.String())
		}

		// Use ResolvedScript for platform-ref sagas (Script is empty, script comes from platform)
		script := saga.Script
		if script == "" {
			script = saga.ResolvedScript
		}
		result, err := h.validator.ValidateActivation(ctx, id, script)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "validation error: %v", err)
		}
		validationResult = h.validationResultToProto(result)

		// If validation blocked, don't proceed with activation
		if result.Status == statusBlocked {
			return nil, status.Errorf(codes.FailedPrecondition, "saga validation failed: %s", result.FormatReport())
		}
	}

	if err := h.registry.ActivateSaga(ctx, id); err != nil {
		return nil, h.mapDomainError(err, "ActivateSaga", id.String())
	}

	// Retrieve the activated definition
	activated, err := h.registry.GetByID(ctx, id)
	if err != nil {
		return nil, h.mapDomainError(err, "ActivateSaga", id.String())
	}

	h.logger.Info("saga activated",
		"id", id,
		"name", activated.Name,
		"version", activated.Version)

	return &sagav1.ActivateSagaResponse{
		Saga:       h.definitionToProto(activated),
		Validation: validationResult,
	}, nil
}

// DeprecateSaga transitions a saga from ACTIVE to DEPRECATED.
func (h *RegistryHandler) DeprecateSaga(
	ctx context.Context,
	req *sagav1.DeprecateSagaRequest,
) (*sagav1.DeprecateSagaResponse, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid saga id: %v", err)
	}

	var successorID *uuid.UUID
	if req.SuccessorId != "" {
		parsed, err := uuid.Parse(req.SuccessorId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid successor_id: %v", err)
		}
		successorID = &parsed
	}

	if err := h.registry.DeprecateSaga(ctx, id, successorID); err != nil {
		return nil, h.mapDomainError(err, "DeprecateSaga", id.String())
	}

	// Retrieve the deprecated definition
	deprecated, err := h.registry.GetByID(ctx, id)
	if err != nil {
		return nil, h.mapDomainError(err, "DeprecateSaga", id.String())
	}

	h.logger.Info("saga deprecated",
		"id", id,
		"name", deprecated.Name,
		"version", deprecated.Version,
		"successorId", req.SuccessorId)

	return &sagav1.DeprecateSagaResponse{
		Saga: h.definitionToProto(deprecated),
	}, nil
}
