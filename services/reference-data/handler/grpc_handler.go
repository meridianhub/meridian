// Package handler implements gRPC service handlers for the reference-data domain.
package handler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// Service errors
var (
	// ErrRegistryNil is returned when attempting to create a service with a nil registry.
	ErrRegistryNil = errors.New("registry cannot be nil")

	// ErrCompilerNil is returned when attempting to create a service with a nil compiler.
	ErrCompilerNil = errors.New("compiler cannot be nil")
)

// Service implements the ReferenceDataService gRPC service.
type Service struct {
	pb.UnimplementedReferenceDataServiceServer
	registry registry.InstrumentRegistry
	compiler *refcel.Compiler
	logger   *slog.Logger
}

// NewService creates a new reference data service.
func NewService(reg registry.InstrumentRegistry, compiler *refcel.Compiler, logger *slog.Logger) (*Service, error) {
	if reg == nil {
		return nil, ErrRegistryNil
	}
	if compiler == nil {
		return nil, ErrCompilerNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &Service{
		registry: reg,
		compiler: compiler,
		logger:   logger,
	}, nil
}

// RegisterInstrument creates a new instrument definition in DRAFT status.
func (s *Service) RegisterInstrument(ctx context.Context, req *pb.RegisterInstrumentRequest) (*pb.RegisterInstrumentResponse, error) {
	// Convert proto to domain
	def := &registry.InstrumentDefinition{
		ID:                       uuid.New(),
		Code:                     req.Code,
		Version:                  1,
		Dimension:                protoDimensionToDomain(req.Dimension),
		Precision:                int(req.Precision),
		Status:                   registry.StatusDraft,
		ValidationExpression:     req.ValidationExpression,
		FungibilityKeyExpression: req.FungibilityKeyExpression,
		ErrorMessageExpression:   req.ErrorMessageExpression,
		AttributeSchema:          []byte(req.AttributeSchema),
		DisplayName:              req.DisplayName,
		Description:              req.Description,
		CreatedAt:                time.Now(),
		UpdatedAt:                time.Now(),
	}

	// Create the draft
	if err := s.registry.CreateDraft(ctx, def); err != nil {
		return nil, s.mapDomainError(err, "RegisterInstrument", req.Code)
	}

	s.logger.Info("instrument registered",
		"code", def.Code,
		"version", def.Version,
		"dimension", def.Dimension)

	return &pb.RegisterInstrumentResponse{
		Instrument: domainToProto(def),
	}, nil
}

// UpdateInstrument modifies a DRAFT instrument definition.
func (s *Service) UpdateInstrument(ctx context.Context, req *pb.UpdateInstrumentRequest) (*pb.UpdateInstrumentResponse, error) {
	// Build the updates
	updates := &registry.InstrumentDefinition{
		ValidationExpression:     req.ValidationExpression,
		FungibilityKeyExpression: req.FungibilityKeyExpression,
		ErrorMessageExpression:   req.ErrorMessageExpression,
		AttributeSchema:          []byte(req.AttributeSchema),
		DisplayName:              req.DisplayName,
		Description:              req.Description,
	}

	// Update the definition
	if err := s.registry.UpdateDefinition(ctx, req.Code, int(req.Version), updates); err != nil {
		return nil, s.mapDomainError(err, "UpdateInstrument", req.Code)
	}

	// Retrieve the updated definition to return
	def, err := s.registry.GetDefinition(ctx, req.Code, int(req.Version))
	if err != nil {
		return nil, s.mapDomainError(err, "UpdateInstrument", req.Code)
	}

	s.logger.Info("instrument updated",
		"code", req.Code,
		"version", req.Version)

	return &pb.UpdateInstrumentResponse{
		Instrument: domainToProto(def),
	}, nil
}

// RetrieveInstrument fetches a specific instrument by code and version.
func (s *Service) RetrieveInstrument(ctx context.Context, req *pb.RetrieveInstrumentRequest) (*pb.RetrieveInstrumentResponse, error) {
	var def *registry.InstrumentDefinition
	var err error

	if req.Version == 0 {
		// Get latest active version
		def, err = s.registry.GetActiveDefinition(ctx, req.Code)
	} else {
		// Get specific version
		def, err = s.registry.GetDefinition(ctx, req.Code, int(req.Version))
	}

	if err != nil {
		return nil, s.mapDomainError(err, "RetrieveInstrument", req.Code)
	}

	return &pb.RetrieveInstrumentResponse{
		Instrument: domainToProto(def),
	}, nil
}

// ListInstruments returns instruments matching the filter criteria with cursor-based pagination.
func (s *Service) ListInstruments(ctx context.Context, req *pb.ListInstrumentsRequest) (*pb.ListInstrumentsResponse, error) {
	cursorTime, cursorID, err := parseCursorToken(req.PageToken)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid page token: %v", err)
	}

	domainStatus := protoStatusToDomain(req.StatusFilter)
	defs, err := s.registry.ListByStatus(ctx, domainStatus)
	if err != nil {
		s.logger.Error("failed to list instruments", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list instruments: %v", err)
	}

	filtered := defs
	sortByCreatedAtDesc(filtered)
	filtered = applyCursorFilter(filtered, cursorTime, cursorID)
	pageSize := normalizePageSize(int(req.PageSize))
	filtered, hasMore := applyPageLimit(filtered, pageSize)

	instruments := make([]*pb.InstrumentDefinition, len(filtered))
	for i, def := range filtered {
		instruments[i] = domainToProto(def)
	}

	return &pb.ListInstrumentsResponse{
		Instruments:   instruments,
		NextPageToken: generateNextPageToken(filtered, hasMore),
	}, nil
}

// sortByCreatedAtDesc sorts instruments by CreatedAt DESC, ID DESC.
func sortByCreatedAtDesc(defs []*registry.InstrumentDefinition) {
	sort.Slice(defs, func(i, j int) bool {
		ti := defs[i].CreatedAt.Truncate(time.Second)
		tj := defs[j].CreatedAt.Truncate(time.Second)
		if ti.Equal(tj) {
			return defs[i].ID.String() > defs[j].ID.String()
		}
		return ti.After(tj)
	})
}

// applyCursorFilter filters instruments to those after the cursor position.
func applyCursorFilter(defs []*registry.InstrumentDefinition, cursorTime time.Time, cursorID uuid.UUID) []*registry.InstrumentDefinition {
	if cursorTime.IsZero() {
		return defs
	}
	cursorTimeCompare := cursorTime.Truncate(time.Second)
	var filtered []*registry.InstrumentDefinition
	for _, def := range defs {
		defTime := def.CreatedAt.Truncate(time.Second)
		if defTime.Before(cursorTimeCompare) ||
			(defTime.Equal(cursorTimeCompare) && def.ID.String() < cursorID.String()) {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

// normalizePageSize applies default and max limits to page size.
func normalizePageSize(pageSize int) int {
	if pageSize == 0 {
		return DefaultPageSize
	}
	if pageSize > MaxPageSize {
		return MaxPageSize
	}
	return pageSize
}

// applyPageLimit returns a page of results and whether more exist.
func applyPageLimit(defs []*registry.InstrumentDefinition, pageSize int) ([]*registry.InstrumentDefinition, bool) {
	if len(defs) <= pageSize {
		return defs, false
	}
	return defs[:pageSize], true
}

// generateNextPageToken creates a token for the next page, or empty if no more results.
func generateNextPageToken(defs []*registry.InstrumentDefinition, hasMore bool) string {
	if !hasMore || len(defs) == 0 {
		return ""
	}
	lastItem := defs[len(defs)-1]
	return encodeCursorToken(lastItem.CreatedAt, lastItem.ID)
}

// ActivateInstrument transitions an instrument from DRAFT to ACTIVE.
func (s *Service) ActivateInstrument(ctx context.Context, req *pb.ActivateInstrumentRequest) (*pb.ActivateInstrumentResponse, error) {
	if err := s.registry.ActivateInstrument(ctx, req.Code, int(req.Version)); err != nil {
		return nil, s.mapDomainError(err, "ActivateInstrument", req.Code)
	}

	// Retrieve the activated definition to return
	def, err := s.registry.GetDefinition(ctx, req.Code, int(req.Version))
	if err != nil {
		return nil, s.mapDomainError(err, "ActivateInstrument", req.Code)
	}

	s.logger.Info("instrument activated",
		"code", req.Code,
		"version", req.Version)

	return &pb.ActivateInstrumentResponse{
		Instrument: domainToProto(def),
	}, nil
}

// DeprecateInstrument transitions an instrument from ACTIVE to DEPRECATED.
func (s *Service) DeprecateInstrument(ctx context.Context, req *pb.DeprecateInstrumentRequest) (*pb.DeprecateInstrumentResponse, error) {
	// Parse optional successor_id from request
	var successorID *uuid.UUID
	if req.SuccessorId != "" {
		parsed, err := uuid.Parse(req.SuccessorId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid successor_id: %v", err)
		}
		successorID = &parsed
	}

	if err := s.registry.DeprecateInstrument(ctx, req.Code, int(req.Version), successorID); err != nil {
		return nil, s.mapDomainError(err, "DeprecateInstrument", req.Code)
	}

	// Retrieve the deprecated definition to return
	def, err := s.registry.GetDefinition(ctx, req.Code, int(req.Version))
	if err != nil {
		return nil, s.mapDomainError(err, "DeprecateInstrument", req.Code)
	}

	s.logger.Info("instrument deprecated",
		"code", req.Code,
		"version", req.Version,
		"successorId", req.SuccessorId)

	return &pb.DeprecateInstrumentResponse{
		Instrument: domainToProto(def),
	}, nil
}

// EvaluateInstrument is a CEL playground endpoint for testing expressions.
func (s *Service) EvaluateInstrument(_ context.Context, req *pb.EvaluateInstrumentRequest) (*pb.EvaluateInstrumentResponse, error) {
	resp := &pb.EvaluateInstrumentResponse{}
	input := buildCELInput(req)
	bucketInput := map[string]any{"attributes": req.TestAttributes}

	// Evaluate each expression
	validationErrors, validationResult := s.evalValidation(req.ValidationExpression, input)
	resp.CompileErrors = append(resp.CompileErrors, validationErrors...)
	resp.ValidationResult = validationResult

	bucketErrors, fungibilityKey := s.evalBucketKey(req.FungibilityKeyExpression, bucketInput)
	resp.CompileErrors = append(resp.CompileErrors, bucketErrors...)
	resp.FungibilityKey = fungibilityKey

	errMsgErrors, errorMessage := s.evalErrorMessage(req.ErrorMessageExpression, input)
	resp.CompileErrors = append(resp.CompileErrors, errMsgErrors...)
	resp.ErrorMessage = errorMessage

	return resp, nil
}

// buildCELInput creates the input map for CEL validation expressions.
func buildCELInput(req *pb.EvaluateInstrumentRequest) map[string]any {
	var validFrom, validTo time.Time
	if req.TestValidFrom != nil {
		validFrom = req.TestValidFrom.AsTime()
	}
	if req.TestValidTo != nil {
		validTo = req.TestValidTo.AsTime()
	}
	return map[string]any{
		"attributes": req.TestAttributes,
		"amount":     req.TestAmount,
		"valid_from": validFrom,
		"valid_to":   validTo,
		"source":     req.TestSource,
	}
}

// evalValidation compiles and evaluates a validation expression.
func (s *Service) evalValidation(expr string, input map[string]any) (errs []string, result bool) {
	if expr == "" {
		return nil, false
	}
	prg, err := s.compiler.CompileValidation(expr)
	if err != nil {
		return []string{"validation_expression: " + err.Error()}, false
	}
	out, _, evalErr := prg.Eval(input)
	if evalErr != nil {
		return []string{"validation_expression eval: " + evalErr.Error()}, false
	}
	if b, ok := out.Value().(bool); ok {
		return nil, b
	}
	return nil, false
}

// evalBucketKey compiles and evaluates a bucket key expression.
func (s *Service) evalBucketKey(expr string, input map[string]any) (errs []string, key string) {
	if expr == "" {
		return nil, ""
	}
	prg, err := s.compiler.CompileBucketKey(expr)
	if err != nil {
		return []string{"fungibility_key_expression: " + err.Error()}, ""
	}
	out, _, evalErr := prg.Eval(input)
	if evalErr != nil {
		return []string{"fungibility_key_expression eval: " + evalErr.Error()}, ""
	}
	if str, ok := out.Value().(string); ok {
		return nil, str
	}
	return nil, ""
}

// evalErrorMessage compiles and evaluates an error message expression.
func (s *Service) evalErrorMessage(expr string, input map[string]any) (errs []string, msg string) {
	if expr == "" {
		return nil, ""
	}
	// Error message expressions return strings, not booleans.
	prg, err := s.compiler.CompileValueExpression(expr)
	if err != nil {
		return []string{"error_message_expression: " + err.Error()}, ""
	}
	out, _, evalErr := prg.Eval(input)
	if evalErr != nil {
		return []string{"error_message_expression eval: " + evalErr.Error()}, ""
	}
	if str, ok := out.Value().(string); ok {
		return nil, str
	}
	return nil, ""
}

// GetAttributeSchema returns the JSON Schema describing valid attributes.
func (s *Service) GetAttributeSchema(ctx context.Context, req *pb.GetAttributeSchemaRequest) (*pb.GetAttributeSchemaResponse, error) {
	// If a specific instrument is requested, return its schema
	if req.Code != "" {
		var def *registry.InstrumentDefinition
		var err error

		if req.Version == 0 {
			def, err = s.registry.GetActiveDefinition(ctx, req.Code)
		} else {
			def, err = s.registry.GetDefinition(ctx, req.Code, int(req.Version))
		}

		if err != nil {
			return nil, s.mapDomainError(err, "GetAttributeSchema", req.Code)
		}

		return &pb.GetAttributeSchemaResponse{
			JsonSchema:        string(def.AttributeSchema),
			InstrumentCode:    def.Code,
			InstrumentVersion: int32(def.Version),
		}, nil
	}

	// Return the default schema for CEL input attributes
	defaultSchema := `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "title": "CEL Attribute Bag",
  "description": "Standard attributes available to CEL expressions for validation and bucket key generation",
  "properties": {
    "attributes": {
      "type": "object",
      "additionalProperties": {
        "type": "string"
      },
      "description": "Key-value attributes from the quantity"
    },
    "amount": {
      "type": "string",
      "description": "Decimal amount as a string for arbitrary precision"
    },
    "valid_from": {
      "type": "string",
      "format": "date-time",
      "description": "Optional validity start time (RFC3339)"
    },
    "valid_to": {
      "type": "string",
      "format": "date-time",
      "description": "Optional validity end time (RFC3339)"
    },
    "source": {
      "type": "string",
      "description": "Origin identifier for the quantity"
    }
  }
}`

	return &pb.GetAttributeSchemaResponse{
		JsonSchema: defaultSchema,
	}, nil
}

// mapDomainError converts domain errors to appropriate gRPC status codes.
func (s *Service) mapDomainError(err error, operation, code string) error {
	switch {
	case errors.Is(err, registry.ErrNotFound):
		s.logger.Warn("instrument not found",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.NotFound, "instrument not found: %s", code)

	case errors.Is(err, registry.ErrSystemInstrumentReadOnly):
		s.logger.Warn("system instrument modification attempted",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.PermissionDenied, "cannot modify system instrument: %s", code)

	case errors.Is(err, registry.ErrNotDraft):
		s.logger.Warn("instrument not in draft status",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.FailedPrecondition, "instrument must be in DRAFT status: %s", code)

	case errors.Is(err, registry.ErrNotActive):
		s.logger.Warn("instrument not in active status",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.FailedPrecondition, "instrument must be in ACTIVE status: %s", code)

	case errors.Is(err, registry.ErrInvalidCEL):
		s.logger.Warn("invalid CEL expression",
			"operation", operation,
			"code", code,
			"error", err)
		return status.Errorf(codes.InvalidArgument, "invalid CEL expression: %v", err)

	case errors.Is(err, registry.ErrAlreadyExists):
		s.logger.Warn("instrument already exists",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.AlreadyExists, "instrument already exists: %s", code)

	case errors.Is(err, registry.ErrOptimisticLock):
		s.logger.Warn("optimistic lock failure",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.Aborted, "instrument was modified by another transaction: %s", code)

	case errors.Is(err, registry.ErrInvalidStateTransition):
		s.logger.Warn("invalid state transition",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.FailedPrecondition, "invalid state transition: %v", err)

	case errors.Is(err, registry.ErrSuccessorInvalid):
		s.logger.Warn("invalid successor instrument",
			"operation", operation,
			"code", code)
		return status.Errorf(codes.FailedPrecondition, "successor instrument is invalid: must exist, be ACTIVE, and have same dimension")

	default:
		s.logger.Error("internal error",
			"operation", operation,
			"code", code,
			"error", err)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// domainToProto converts a domain InstrumentDefinition to proto.
func domainToProto(def *registry.InstrumentDefinition) *pb.InstrumentDefinition {
	if def == nil {
		return nil
	}

	proto := &pb.InstrumentDefinition{
		Id:                       def.ID.String(),
		Code:                     def.Code,
		Version:                  int32(def.Version),
		Dimension:                domainDimensionToProto(def.Dimension),
		Precision:                int32(def.Precision),
		Status:                   domainStatusToProto(def.Status),
		ValidationExpression:     def.ValidationExpression,
		FungibilityKeyExpression: def.FungibilityKeyExpression,
		ErrorMessageExpression:   def.ErrorMessageExpression,
		AttributeSchema:          string(def.AttributeSchema),
		DisplayName:              def.DisplayName,
		Description:              def.Description,
		CreatedAt:                timestamppb.New(def.CreatedAt),
		IsSystem:                 def.IsSystem,
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

// domainStatusToProto converts domain Status to proto InstrumentStatus.
func domainStatusToProto(s registry.Status) pb.InstrumentStatus {
	switch s {
	case registry.StatusDraft:
		return pb.InstrumentStatus_INSTRUMENT_STATUS_DRAFT
	case registry.StatusActive:
		return pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE
	case registry.StatusDeprecated:
		return pb.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED
	default:
		return pb.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED
	}
}

// protoStatusToDomain converts proto InstrumentStatus to domain Status.
func protoStatusToDomain(s pb.InstrumentStatus) registry.Status {
	switch s {
	case pb.InstrumentStatus_INSTRUMENT_STATUS_DRAFT:
		return registry.StatusDraft
	case pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE:
		return registry.StatusActive
	case pb.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED:
		return registry.StatusDeprecated
	case pb.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// domainDimensionToProto converts domain Dimension to proto Dimension.
func domainDimensionToProto(d registry.Dimension) pb.Dimension {
	switch d {
	case registry.DimensionMonetary:
		return pb.Dimension_DIMENSION_CURRENCY
	case registry.DimensionEnergy:
		return pb.Dimension_DIMENSION_ENERGY
	case registry.DimensionMass:
		return pb.Dimension_DIMENSION_MASS
	case registry.DimensionVolume:
		return pb.Dimension_DIMENSION_VOLUME
	case registry.DimensionTime:
		return pb.Dimension_DIMENSION_TIME
	case registry.DimensionCompute:
		return pb.Dimension_DIMENSION_COMPUTE
	case registry.DimensionQuantity:
		return pb.Dimension_DIMENSION_COUNT
	case registry.DimensionCarbon:
		return pb.Dimension_DIMENSION_CARBON
	case registry.DimensionData:
		return pb.Dimension_DIMENSION_DATA
	default:
		return pb.Dimension_DIMENSION_UNSPECIFIED
	}
}

// protoDimensionToDomain converts proto Dimension to domain Dimension.
func protoDimensionToDomain(d pb.Dimension) registry.Dimension {
	switch d {
	case pb.Dimension_DIMENSION_CURRENCY:
		return registry.DimensionMonetary
	case pb.Dimension_DIMENSION_ENERGY:
		return registry.DimensionEnergy
	case pb.Dimension_DIMENSION_MASS:
		return registry.DimensionMass
	case pb.Dimension_DIMENSION_VOLUME:
		return registry.DimensionVolume
	case pb.Dimension_DIMENSION_TIME:
		return registry.DimensionTime
	case pb.Dimension_DIMENSION_COMPUTE:
		return registry.DimensionCompute
	case pb.Dimension_DIMENSION_COUNT:
		return registry.DimensionQuantity
	case pb.Dimension_DIMENSION_CARBON:
		return registry.DimensionCarbon
	case pb.Dimension_DIMENSION_DATA:
		return registry.DimensionData
	case pb.Dimension_DIMENSION_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}
