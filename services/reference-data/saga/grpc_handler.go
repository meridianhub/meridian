// Package saga provides gRPC handlers for saga definition management.
package saga

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/pkg/saga/validation"
)

// RegistryHandler implements the SagaRegistryService gRPC service.
// It provides management operations for saga definitions including
// creating drafts, updating definitions, activation, and deprecation.
type RegistryHandler struct {
	sagav1.UnimplementedSagaRegistryServiceServer
	registry        Registry
	validator       *ReferenceValidator
	dryRunValidator *validation.DryRunValidator
	schemaRegistry  *schema.Registry
	logger          *slog.Logger
}

// NewRegistryHandler creates a new saga registry gRPC handler.
// The validator is optional - if nil, ValidateSagaDraft will return empty results.
// The dryRunValidator is optional - if nil, ValidateSaga will return an error.
// The schemaRegistry is optional - use WithSchemaRegistry or WithDerivedSchema to populate.
// If not set, DescribeHandlers will return an empty handler list.
func NewRegistryHandler(registry Registry, validator *ReferenceValidator, dryRunValidator *validation.DryRunValidator, logger *slog.Logger) *RegistryHandler {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &RegistryHandler{
		registry:        registry,
		validator:       validator,
		dryRunValidator: dryRunValidator,
		logger:          logger,
	}
}

// WithSchemaRegistry sets the schema registry on the handler.
// Used to inject a pre-loaded registry instead of loading the default.
func (h *RegistryHandler) WithSchemaRegistry(reg *schema.Registry) *RegistryHandler {
	h.schemaRegistry = reg
	return h
}

// WithDerivedSchema populates the schema registry from a proto-derived Schema.
// This is the preferred method for production use.
func (h *RegistryHandler) WithDerivedSchema(s *schema.Schema) *RegistryHandler {
	h.schemaRegistry = schema.NewRegistryFromSchema(s)
	return h
}

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
		if req.Version < 0 {
			return nil, status.Errorf(codes.InvalidArgument, "version must be >= 0")
		}
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
	defs, err := h.fetchSagas(ctx, req.StatusFilter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list sagas: %v", err)
	}

	// Filter out system sagas if requested
	if req.ExcludeSystem {
		defs = h.filterNonSystemSagas(defs)
	}

	// Sort for stable pagination
	h.sortDefinitions(defs)

	// Parse and validate pagination
	offset, err := h.parsePageToken(req.PageToken)
	if err != nil {
		return nil, err
	}

	// Apply pagination
	pageSize := h.normalizePageSize(int(req.PageSize))
	paginatedDefs, nextToken := h.paginate(defs, offset, pageSize)

	// Convert to proto
	sagas := make([]*sagav1.SagaDefinition, len(paginatedDefs))
	for i, def := range paginatedDefs {
		sagas[i] = h.definitionToProto(def)
	}

	return &sagav1.ListSagasResponse{
		Sagas:         sagas,
		NextPageToken: nextToken,
	}, nil
}

// fetchSagas retrieves sagas filtered by status.
func (h *RegistryHandler) fetchSagas(ctx context.Context, statusFilter sagav1.SagaStatus) ([]*Definition, error) {
	if statusFilter != sagav1.SagaStatus_SAGA_STATUS_UNSPECIFIED {
		domainStatus := h.protoStatusToDomain(statusFilter)
		return h.registry.ListByStatus(ctx, domainStatus)
	}

	// Get all sagas by listing each status
	drafts, err1 := h.registry.ListByStatus(ctx, StatusDraft)
	actives, err2 := h.registry.ListByStatus(ctx, StatusActive)
	deprecated, err3 := h.registry.ListByStatus(ctx, StatusDeprecated)

	if err1 != nil {
		return nil, err1
	}
	if err2 != nil {
		return nil, err2
	}
	if err3 != nil {
		return nil, err3
	}

	defs := make([]*Definition, 0, len(drafts)+len(actives)+len(deprecated))
	defs = append(defs, drafts...)
	defs = append(defs, actives...)
	defs = append(defs, deprecated...)
	return defs, nil
}

// filterNonSystemSagas returns only non-system sagas.
func (h *RegistryHandler) filterNonSystemSagas(defs []*Definition) []*Definition {
	filtered := make([]*Definition, 0, len(defs))
	for _, def := range defs {
		if !def.IsSystem {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

// sortDefinitions sorts definitions by name, then version for stable pagination.
func (h *RegistryHandler) sortDefinitions(defs []*Definition) {
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Name == defs[j].Name {
			return defs[i].Version < defs[j].Version
		}
		return defs[i].Name < defs[j].Name
	})
}

// parsePageToken parses the offset from a page token.
func (h *RegistryHandler) parsePageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(token)
	if err != nil || offset < 0 {
		return 0, status.Error(codes.InvalidArgument, "invalid page_token")
	}
	return offset, nil
}

// normalizePageSize ensures page size is within valid bounds.
func (h *RegistryHandler) normalizePageSize(pageSize int) int {
	if pageSize <= 0 {
		return 50
	}
	if pageSize > 100 {
		return 100
	}
	return pageSize
}

// paginate returns a slice of definitions and the next page token.
func (h *RegistryHandler) paginate(defs []*Definition, offset, pageSize int) ([]*Definition, string) {
	if offset > len(defs) {
		offset = len(defs)
	}

	end := offset + pageSize
	if end > len(defs) {
		end = len(defs)
	}

	nextToken := ""
	if end < len(defs) {
		nextToken = strconv.Itoa(end)
	}

	return defs[offset:end], nextToken
}

// ValidateSaga validates a saga script using dry-run execution.
// This validates script syntax, handler existence, type correctness, and runtime behavior.
func (h *RegistryHandler) ValidateSaga(
	ctx context.Context,
	req *sagav1.ValidateSagaRequest,
) (*sagav1.ValidateSagaResponse, error) {
	// Return error if validator not configured
	if h.dryRunValidator == nil {
		return nil, status.Error(codes.FailedPrecondition, "dry-run validator not configured")
	}

	// Validate the script using DryRunValidator
	result, err := h.dryRunValidator.Validate(ctx, req.Script)
	if err != nil {
		h.logger.Error("validation failed",
			"saga_name", req.SagaName,
			"version", req.Version,
			"error", err)
		return nil, status.Errorf(codes.Internal, "validation error: %v", err)
	}

	// Record validation metrics
	validation.RecordValidation(req.SagaName, result)

	// Log validation attempt
	h.logger.Info("saga script validated",
		"saga_name", req.SagaName,
		"version", req.Version,
		"success", result.Success,
		"error_count", len(result.Errors),
		"handler_count", result.Metrics.HandlerCallCount)

	// Convert ValidationResult to protobuf response
	response := &sagav1.ValidateSagaResponse{
		Success: result.Success,
		Errors:  make([]*sagav1.ValidationError, 0, len(result.Errors)),
		Metrics: &sagav1.ComplexityMetrics{
			HandlerCallCount:    int32(result.Metrics.HandlerCallCount),
			OperationCount:      int32(result.Metrics.OperationCount),
			EstimatedDurationMs: int32(result.Metrics.EstimatedDuration.Milliseconds()),
			ComplexityScore:     int32(calculateComplexityScore(result.Metrics.HandlerCallCount)),
		},
	}

	// Convert errors
	for _, err := range result.Errors {
		response.Errors = append(response.Errors, &sagav1.ValidationError{
			Line:       int32(err.Line),
			Column:     int32(err.Column),
			Message:    err.Message,
			Category:   h.errorCategoryToProto(err.Category),
			Suggestion: "", // Could enhance with handler suggestions later
		})
	}

	// Generate formatted report using HumanReadableFormatter
	formatter := &validation.HumanReadableFormatter{
		AvailableHandlers: []string{}, // Could inject from schema registry
	}
	response.FormattedReport = formatter.Format(result)

	return response, nil
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

	// Use ResolvedScript for platform-ref sagas (Script is empty, script comes from platform)
	validationScript := saga.Script
	if validationScript == "" {
		validationScript = saga.ResolvedScript
	}
	result, err := h.validator.ValidateDraft(ctx, id, validationScript)
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

// errorCategoryToProto converts validation.ErrorCategory to proto ErrorCategory.
func (h *RegistryHandler) errorCategoryToProto(cat validation.ErrorCategory) sagav1.ErrorCategory {
	switch cat {
	case validation.CategorySyntax:
		return sagav1.ErrorCategory_ERROR_CATEGORY_SYNTAX
	case validation.CategoryUndefinedHandler:
		return sagav1.ErrorCategory_ERROR_CATEGORY_UNDEFINED_HANDLER
	case validation.CategoryTypeMismatch:
		return sagav1.ErrorCategory_ERROR_CATEGORY_TYPE_MISMATCH
	case validation.CategoryRuntime:
		return sagav1.ErrorCategory_ERROR_CATEGORY_RUNTIME
	case validation.CategoryTimeout:
		return sagav1.ErrorCategory_ERROR_CATEGORY_TIMEOUT
	default:
		return sagav1.ErrorCategory_ERROR_CATEGORY_UNSPECIFIED
	}
}

// calculateComplexityScore calculates a 0-10 complexity score based on handler call count.
// Formula: min(10, HandlerCallCount / 2)
func calculateComplexityScore(handlerCallCount int) int {
	score := handlerCallCount / 2
	if score > 10 {
		return 10
	}
	return score
}

// DescribeHandlers returns the platform handler schema registry grouped by service.
// Used by the Starlark editor to display available handlers and their parameter types.
func (h *RegistryHandler) DescribeHandlers(
	_ context.Context,
	_ *sagav1.DescribeHandlersRequest,
) (*sagav1.DescribeHandlersResponse, error) {
	reg := h.schemaRegistry
	if reg == nil {
		reg = schema.NewRegistry()
	}

	// Group handlers by service name (first component of "service.handler" name)
	handlerNames := reg.ListHandlers()
	serviceMap := make(map[string][]*sagav1.HandlerSchema)
	serviceOrder := make([]string, 0)

	for _, fullName := range handlerNames {
		parts := strings.SplitN(fullName, ".", 2)
		if len(parts) < 2 {
			continue
		}
		serviceName := parts[0]
		handlerName := parts[1]

		def, err := reg.GetHandler(fullName)
		if err != nil {
			continue
		}

		params := make([]*sagav1.HandlerParameter, 0, len(def.Params))
		// Sort parameter names for deterministic output
		paramNames := make([]string, 0, len(def.Params))
		for paramName := range def.Params {
			paramNames = append(paramNames, paramName)
		}
		sort.Strings(paramNames)

		for _, paramName := range paramNames {
			paramDef := def.Params[paramName]
			params = append(params, &sagav1.HandlerParameter{
				Name:        paramName,
				Type:        string(paramDef.Type),
				Required:    paramDef.Required,
				EnumValues:  paramDef.Values,
				Description: paramDef.Description,
			})
		}

		if _, exists := serviceMap[serviceName]; !exists {
			serviceOrder = append(serviceOrder, serviceName)
		}
		serviceMap[serviceName] = append(serviceMap[serviceName], &sagav1.HandlerSchema{
			Name:              handlerName,
			Description:       def.Description,
			Parameters:        params,
			IsExternal:        def.External,
			CompensateHandler: def.Compensate,
		})
	}

	sort.Strings(serviceOrder)

	services := make([]*sagav1.HandlerServiceSchema, 0, len(serviceOrder))
	for _, serviceName := range serviceOrder {
		services = append(services, &sagav1.HandlerServiceSchema{
			Name:     serviceName,
			Handlers: serviceMap[serviceName],
		})
	}

	return &sagav1.DescribeHandlersResponse{
		Services: services,
	}, nil
}
