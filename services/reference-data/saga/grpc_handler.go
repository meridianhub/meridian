// Package saga provides gRPC handlers for saga definition management.
package saga

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
)

// RegistryHandler implements the SagaRegistryService gRPC service.
// It provides management operations for saga definitions including
// creating drafts, updating definitions, activation, and deprecation.
type RegistryHandler struct {
	sagav1.UnimplementedSagaRegistryServiceServer
	registry  Registry
	validator *ReferenceValidator
	logger    *slog.Logger
}

// NewRegistryHandler creates a new saga registry gRPC handler.
// The validator is optional - if nil, ValidateSagaDraft will return empty results.
func NewRegistryHandler(registry Registry, validator *ReferenceValidator, logger *slog.Logger) *RegistryHandler {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &RegistryHandler{
		registry:  registry,
		validator: validator,
		logger:    logger,
	}
}

// CreateSagaDraft creates a new saga definition in DRAFT status.
func (h *RegistryHandler) CreateSagaDraft(
	ctx context.Context,
	req *sagav1.CreateSagaDraftRequest,
) (*sagav1.CreateSagaDraftResponse, error) {
	version := int(req.Version)
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

	if err := h.registry.CreateDraft(ctx, def); err != nil {
		return nil, h.mapDomainError(err, "CreateSagaDraft", req.Name)
	}

	h.logger.Info("saga draft created",
		"name", def.Name,
		"version", def.Version,
		"id", def.ID)

	// Perform initial validation (non-blocking)
	var validationResult *sagav1.ValidationResult
	if h.validator != nil {
		result, err := h.validator.ValidateDraft(ctx, def.ID, def.Script)
		if err != nil {
			h.logger.Warn("validation failed during draft creation",
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
func (h *RegistryHandler) UpdateSagaDefinition(
	ctx context.Context,
	req *sagav1.UpdateSagaDefinitionRequest,
) (*sagav1.UpdateSagaDefinitionResponse, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid saga id: %v", err)
	}

	updates := &Definition{
		Script:                  req.Script,
		PreconditionsExpression: req.PreconditionsExpression,
		DisplayName:             req.DisplayName,
		Description:             req.Description,
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

		result, err := h.validator.ValidateActivation(ctx, id, saga.Script)
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

// GetSaga retrieves a specific saga by ID or by name+version.
func (h *RegistryHandler) GetSaga(
	ctx context.Context,
	req *sagav1.GetSagaRequest,
) (*sagav1.GetSagaResponse, error) {
	var def *Definition
	var err error

	if req.Id != "" {
		id, parseErr := uuid.Parse(req.Id)
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid saga id: %v", parseErr)
		}
		def, err = h.registry.GetByID(ctx, id)
	} else if req.Name != "" {
		if req.Version == 0 {
			def, err = h.registry.GetActive(ctx, req.Name)
		} else {
			def, err = h.registry.GetDefinition(ctx, req.Name, int(req.Version))
		}
	} else {
		return nil, status.Errorf(codes.InvalidArgument, "either id or name must be provided")
	}

	if err != nil {
		identifier := req.Id
		if identifier == "" {
			identifier = req.Name
		}
		return nil, h.mapDomainError(err, "GetSaga", identifier)
	}

	return &sagav1.GetSagaResponse{
		Saga: h.definitionToProto(def),
	}, nil
}

// GetActiveSaga retrieves the active saga for a name using tenant resolution.
func (h *RegistryHandler) GetActiveSaga(
	ctx context.Context,
	req *sagav1.GetActiveSagaRequest,
) (*sagav1.GetActiveSagaResponse, error) {
	def, err := h.registry.GetActive(ctx, req.Name)
	if err != nil {
		return nil, h.mapDomainError(err, "GetActiveSaga", req.Name)
	}

	return &sagav1.GetActiveSagaResponse{
		Saga:             h.definitionToProto(def),
		IsTenantOverride: !def.IsSystem,
	}, nil
}

// ListSagas retrieves sagas matching the filter criteria.
func (h *RegistryHandler) ListSagas(
	ctx context.Context,
	req *sagav1.ListSagasRequest,
) (*sagav1.ListSagasResponse, error) {
	var defs []*Definition
	var err error

	// Filter by status if specified
	if req.StatusFilter != sagav1.SagaStatus_SAGA_STATUS_UNSPECIFIED {
		domainStatus := h.protoStatusToDomain(req.StatusFilter)
		defs, err = h.registry.ListByStatus(ctx, domainStatus)
	} else {
		// Get all sagas by listing each status
		drafts, err1 := h.registry.ListByStatus(ctx, StatusDraft)
		actives, err2 := h.registry.ListByStatus(ctx, StatusActive)
		deprecated, err3 := h.registry.ListByStatus(ctx, StatusDeprecated)

		if err1 != nil || err2 != nil || err3 != nil {
			return nil, status.Errorf(codes.Internal, "failed to list sagas")
		}
		defs = append(defs, drafts...)
		defs = append(defs, actives...)
		defs = append(defs, deprecated...)
	}

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list sagas: %v", err)
	}

	// Filter out system sagas if requested
	if !req.IncludeSystem {
		filtered := make([]*Definition, 0, len(defs))
		for _, def := range defs {
			if !def.IsSystem {
				filtered = append(filtered, def)
			}
		}
		defs = filtered
	}

	// Apply pagination (simple implementation)
	pageSize := int(req.PageSize)
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// TODO: Implement proper cursor-based pagination
	if len(defs) > pageSize {
		defs = defs[:pageSize]
	}

	sagas := make([]*sagav1.SagaDefinition, len(defs))
	for i, def := range defs {
		sagas[i] = h.definitionToProto(def)
	}

	return &sagav1.ListSagasResponse{
		Sagas:         sagas,
		NextPageToken: "", // TODO: Implement pagination
	}, nil
}

// ValidateSagaDraft validates a saga script without activating it.
func (h *RegistryHandler) ValidateSagaDraft(
	ctx context.Context,
	req *sagav1.ValidateSagaDraftRequest,
) (*sagav1.ValidateSagaDraftResponse, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid saga id: %v", err)
	}

	// Get the saga
	saga, err := h.registry.GetByID(ctx, id)
	if err != nil {
		return nil, h.mapDomainError(err, "ValidateSagaDraft", id.String())
	}

	// Validate
	if h.validator == nil {
		return &sagav1.ValidateSagaDraftResponse{
			Validation: &sagav1.ValidationResult{
				Status: "READY",
			},
			Report: "No validator configured",
		}, nil
	}

	result, err := h.validator.ValidateDraft(ctx, id, saga.Script)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "validation error: %v", err)
	}

	return &sagav1.ValidateSagaDraftResponse{
		Validation: h.validationResultToProto(result),
		Report:     result.FormatReport(),
	}, nil
}

// AnalyzeDeprecationImpact finds all sagas that reference a given instrument.
func (h *RegistryHandler) AnalyzeDeprecationImpact(
	ctx context.Context,
	req *sagav1.AnalyzeDeprecationImpactRequest,
) (*sagav1.AnalyzeDeprecationImpactResponse, error) {
	if h.validator == nil {
		return &sagav1.AnalyzeDeprecationImpactResponse{
			Dependencies: nil,
			TotalCount:   0,
		}, nil
	}

	deps, err := h.validator.DeprecationImpactAnalysis(ctx, req.InstrumentCode)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to analyze deprecation impact: %v", err)
	}

	protoDeps := make([]*sagav1.SagaDependency, len(deps))
	for i, dep := range deps {
		protoDeps[i] = &sagav1.SagaDependency{
			SagaId:      dep.SagaID.String(),
			SagaName:    dep.SagaName,
			SagaVersion: int32(dep.SagaVersion),
			SagaStatus:  h.domainStatusToProto(dep.SagaStatus),
			LineNumber:  int32(dep.LineNumber),
		}
	}

	return &sagav1.AnalyzeDeprecationImpactResponse{
		Dependencies: protoDeps,
		TotalCount:   int32(len(deps)),
	}, nil
}

// mapDomainError converts domain errors to appropriate gRPC status codes.
func (h *RegistryHandler) mapDomainError(err error, operation, identifier string) error {
	switch {
	case errors.Is(err, ErrNotFound):
		h.logger.Warn("saga not found",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.NotFound, "saga not found: %s", identifier)

	case errors.Is(err, ErrSystemSagaReadOnly):
		h.logger.Warn("system saga modification attempted",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.PermissionDenied, "cannot modify system saga: %s", identifier)

	case errors.Is(err, ErrNotDraft):
		h.logger.Warn("saga not in draft status",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.FailedPrecondition, "saga must be in DRAFT status: %s", identifier)

	case errors.Is(err, ErrNotActive):
		h.logger.Warn("saga not in active status",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.FailedPrecondition, "saga must be in ACTIVE status: %s", identifier)

	case errors.Is(err, ErrAlreadyExists):
		h.logger.Warn("saga already exists",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.AlreadyExists, "saga already exists: %s", identifier)

	case errors.Is(err, ErrOptimisticLock):
		h.logger.Warn("optimistic lock failure",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.Aborted, "saga was modified by another transaction: %s", identifier)

	case errors.Is(err, ErrInvalidStateTransition):
		h.logger.Warn("invalid state transition",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.FailedPrecondition, "invalid state transition: %v", err)

	case errors.Is(err, ErrSuccessorInvalid):
		h.logger.Warn("invalid successor saga",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.FailedPrecondition, "successor saga is invalid: must exist, be ACTIVE, and have same name")

	case errors.Is(err, ErrValidationFailed):
		h.logger.Warn("saga validation failed",
			"operation", operation,
			"identifier", identifier,
			"error", err)
		return status.Errorf(codes.FailedPrecondition, "saga validation failed: %v", err)

	default:
		h.logger.Error("internal error",
			"operation", operation,
			"identifier", identifier,
			"error", err)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// definitionToProto converts a domain Definition to proto SagaDefinition.
func (h *RegistryHandler) definitionToProto(def *Definition) *sagav1.SagaDefinition {
	if def == nil {
		return nil
	}

	proto := &sagav1.SagaDefinition{
		Id:                      def.ID.String(),
		Name:                    def.Name,
		Version:                 int32(def.Version),
		Script:                  def.Script,
		Status:                  h.domainStatusToProto(def.Status),
		IsSystem:                def.IsSystem,
		PreconditionsExpression: def.PreconditionsExpression,
		DisplayName:             def.DisplayName,
		Description:             def.Description,
		CreatedAt:               timestamppb.New(def.CreatedAt),
		UpdatedAt:               timestamppb.New(def.UpdatedAt),
	}

	if def.ActivatedAt != nil {
		proto.ActivatedAt = timestamppb.New(*def.ActivatedAt)
	}
	if def.DeprecatedAt != nil {
		proto.DeprecatedAt = timestamppb.New(*def.DeprecatedAt)
	}
	if def.SuccessorID != nil {
		proto.SuccessorId = def.SuccessorID.String()
	}

	return proto
}

// domainStatusToProto converts domain Status to proto SagaStatus.
func (h *RegistryHandler) domainStatusToProto(s Status) sagav1.SagaStatus {
	switch s {
	case StatusDraft:
		return sagav1.SagaStatus_SAGA_STATUS_DRAFT
	case StatusActive:
		return sagav1.SagaStatus_SAGA_STATUS_ACTIVE
	case StatusDeprecated:
		return sagav1.SagaStatus_SAGA_STATUS_DEPRECATED
	default:
		return sagav1.SagaStatus_SAGA_STATUS_UNSPECIFIED
	}
}

// protoStatusToDomain converts proto SagaStatus to domain Status.
func (h *RegistryHandler) protoStatusToDomain(s sagav1.SagaStatus) Status {
	switch s {
	case sagav1.SagaStatus_SAGA_STATUS_DRAFT:
		return StatusDraft
	case sagav1.SagaStatus_SAGA_STATUS_ACTIVE:
		return StatusActive
	case sagav1.SagaStatus_SAGA_STATUS_DEPRECATED:
		return StatusDeprecated
	case sagav1.SagaStatus_SAGA_STATUS_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// validationResultToProto converts a domain ValidationResult to proto.
func (h *RegistryHandler) validationResultToProto(result *ValidationResult) *sagav1.ValidationResult {
	if result == nil {
		return nil
	}

	proto := &sagav1.ValidationResult{
		Status:         result.Status,
		Warnings:       make([]*sagav1.ValidationWarning, 0),
		CriticalErrors: make([]*sagav1.ValidationWarning, 0),
	}

	for _, err := range result.Errors {
		warning := &sagav1.ValidationWarning{
			ReferenceType: string(err.Reference.Type),
			ReferenceKey:  err.Reference.Key,
			LineNumber:    int32(err.Reference.LineNumber),
			Message:       err.Message,
			Suggestion:    err.Suggestion,
		}
		if err.IsCritical {
			proto.CriticalErrors = append(proto.CriticalErrors, warning)
		} else {
			proto.Warnings = append(proto.Warnings, warning)
		}
	}

	return proto
}
