// Package saga provides gRPC handlers for saga definition management.
package saga

import (
	"errors"
	"log/slog"
	"os"

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
