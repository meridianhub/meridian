package saga

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/pkg/saga/validation"
)

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

// DescribeHandlers returns the platform handler schema registry grouped by service.
// Used by the Starlark editor to display available handlers and their parameter types.
func (h *RegistryHandler) DescribeHandlers(
	_ context.Context,
	_ *sagav1.DescribeHandlersRequest,
) (*sagav1.DescribeHandlersResponse, error) {
	reg := h.schemaRegistry
	if reg == nil {
		h.logger.Warn("DescribeHandlers called without schema registry; returning empty handler list. " +
			"Use WithSchemaRegistry or WithDerivedSchema to populate handlers.")
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
