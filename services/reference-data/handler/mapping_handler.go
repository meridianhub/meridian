package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/reference-data/mapping"
	sharedmapping "github.com/meridianhub/meridian/shared/pkg/mapping"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// MappingService implements MappingServiceServer for MappingDefinition CRUD and DryRunMapping.
type MappingService struct {
	pb.UnimplementedMappingServiceServer
	repo      mapping.Repository
	validator *mapping.Validator
	engine    *sharedmapping.Engine
	logger    *slog.Logger
}

// ErrMappingRepoNil is returned when a nil repository is provided.
var ErrMappingRepoNil = errors.New("mapping repository cannot be nil")

// NewMappingService creates a new MappingService.
// A mapping engine is created automatically for DryRunMapping support.
func NewMappingService(repo mapping.Repository, validator *mapping.Validator, logger *slog.Logger) (*MappingService, error) {
	if repo == nil {
		return nil, ErrMappingRepoNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	engine, err := sharedmapping.NewEngine()
	if err != nil {
		return nil, fmt.Errorf("creating mapping engine: %w", err)
	}
	return &MappingService{
		repo:      repo,
		validator: validator,
		engine:    engine,
		logger:    logger,
	}, nil
}

// CreateMapping creates a new mapping definition in DRAFT status.
func (s *MappingService) CreateMapping(ctx context.Context, req *pb.CreateMappingRequest) (*pb.CreateMappingResponse, error) {
	def := &mapping.Definition{
		Name:                  req.GetName(),
		TargetService:         req.GetTargetService(),
		TargetRPC:             req.GetTargetRpc(),
		Version:               int(req.GetVersion()),
		ExternalSchema:        req.GetExternalSchema(),
		Fields:                protoFieldsToCorrespondences(req.GetFields()),
		InboundComputed:       protoComputedFieldsToDomain(req.GetInboundComputedFields()),
		OutboundComputed:      protoComputedFieldsToDomain(req.GetOutboundComputedFields()),
		InboundValidationCEL:  req.GetInboundValidationCel(),
		OutboundValidationCEL: req.GetOutboundValidationCel(),
		IsBatch:               req.GetIsBatch(),
		BatchTargetPath:       req.GetBatchTargetPath(),
		Idempotency:           protoIdempotencyToDomain(req.GetIdempotency()),
	}

	if s.validator != nil {
		if err := s.validator.Validate(def); err != nil {
			return nil, s.mapDomainError(ctx, err, "CreateMapping", def.Name)
		}
	}

	if err := s.repo.Create(ctx, def); err != nil {
		return nil, s.mapDomainError(ctx, err, "CreateMapping", def.Name)
	}

	s.logger.Info("mapping created", "id", def.ID, "name", def.Name)

	return &pb.CreateMappingResponse{
		Mapping: mappingToProto(def),
	}, nil
}

// GetMapping retrieves a mapping definition by ID.
func (s *MappingService) GetMapping(ctx context.Context, req *pb.GetMappingRequest) (*pb.GetMappingResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	def, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "GetMapping", req.GetId())
	}

	return &pb.GetMappingResponse{
		Mapping: mappingToProto(def),
	}, nil
}

// ListMappings returns mapping definitions for the tenant with optional filtering.
func (s *MappingService) ListMappings(ctx context.Context, req *pb.ListMappingsRequest) (*pb.ListMappingsResponse, error) {
	statusFilter := protoStatusToDomainMapping(req.GetStatus())
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	defs, total, err := s.repo.ListByTenant(ctx, statusFilter, req.GetTargetService(), pageSize, req.GetPageToken())
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "ListMappings", "")
	}

	mappings := make([]*pb.MappingDefinition, len(defs))
	for i, def := range defs {
		mappings[i] = mappingToProto(def)
	}

	var nextPageToken string
	if len(defs) == pageSize {
		nextPageToken = defs[len(defs)-1].ID.String()
	}

	return &pb.ListMappingsResponse{
		Mappings:      mappings,
		NextPageToken: nextPageToken,
		TotalCount:    int32(total),
	}, nil
}

// UpdateMapping modifies an existing DRAFT mapping definition.
func (s *MappingService) UpdateMapping(ctx context.Context, req *pb.UpdateMappingRequest) (*pb.UpdateMappingResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateMapping", req.GetId())
	}

	// Apply field mask or full replace
	updated := applyUpdateMask(existing, req)

	if s.validator != nil {
		if err := s.validator.Validate(updated); err != nil {
			return nil, s.mapDomainError(ctx, err, "UpdateMapping", req.GetId())
		}
	}

	if err := s.repo.Update(ctx, updated, existing.UpdatedAt); err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateMapping", req.GetId())
	}

	s.logger.Info("mapping updated", "id", id)

	return &pb.UpdateMappingResponse{
		Mapping: mappingToProto(updated),
	}, nil
}

// DeleteMapping removes a mapping definition. Returns FAILED_PRECONDITION if ACTIVE.
func (s *MappingService) DeleteMapping(ctx context.Context, req *pb.DeleteMappingRequest) (*pb.DeleteMappingResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return nil, s.mapDomainError(ctx, err, "DeleteMapping", req.GetId())
	}

	s.logger.Info("mapping deleted", "id", id)

	return &pb.DeleteMappingResponse{Id: req.GetId()}, nil
}

// DryRunMapping executes a mapping definition against sample JSON for testing purposes.
// No data is persisted. Returns the transformed output, validation result, and field-level trace.
func (s *MappingService) DryRunMapping(ctx context.Context, req *pb.DryRunMappingRequest) (*pb.DryRunMappingResponse, error) {
	def, err := s.resolveMappingForDryRun(ctx, req.GetMappingName(), int(req.GetMappingVersion()))
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "DryRunMapping", req.GetMappingName())
	}

	protoMapping := mappingToProto(def)
	sampleJSON := []byte(req.GetSampleJson())

	var dryResult *sharedmapping.DryRunResult
	switch req.GetDirection() {
	case "inbound":
		dryResult = s.engine.DryRunInbound(protoMapping, sampleJSON)
	case "outbound":
		dryResult = s.engine.DryRunOutbound(protoMapping, sampleJSON)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "direction must be 'inbound' or 'outbound'")
	}

	return &pb.DryRunMappingResponse{
		TransformedJson:   dryResult.TransformedJSON,
		IdempotencyKey:    dryResult.IdempotencyKey,
		ValidationResult:  dryRunValidationToProto(dryResult),
		ExecutionTimeMs:   dryResult.ExecutionTimeMs,
		FieldMappingTrace: fieldTracesToProto(dryResult.FieldTraces),
	}, nil
}

// resolveMappingForDryRun looks up a mapping definition by name and optional version.
// If version is 0, returns the latest ACTIVE definition. Otherwise returns the definition
// with the exact version regardless of status.
func (s *MappingService) resolveMappingForDryRun(ctx context.Context, name string, version int) (*mapping.Definition, error) {
	if version == 0 {
		return s.repo.GetLatestActive(ctx, name)
	}
	return s.repo.GetByNameAndVersion(ctx, name, version)
}

func dryRunValidationToProto(r *sharedmapping.DryRunResult) *pb.DryRunValidationResult {
	return &pb.DryRunValidationResult{
		Passed: r.ValidationPassed,
		Errors: r.ValidationErrors,
	}
}

func fieldTracesToProto(traces []sharedmapping.FieldTrace) []*pb.FieldMappingTrace {
	if len(traces) == 0 {
		return nil
	}
	result := make([]*pb.FieldMappingTrace, len(traces))
	for i, t := range traces {
		result[i] = &pb.FieldMappingTrace{
			SourcePath:       t.SourcePath,
			TargetPath:       t.TargetPath,
			SourceValue:      t.SourceValue,
			TransformedValue: t.TransformedValue,
			TransformType:    t.TransformType,
		}
	}
	return result
}

// --- Helpers ---

func applyUpdateMask(existing *mapping.Definition, req *pb.UpdateMappingRequest) *mapping.Definition {
	updated := *existing

	mask := req.GetUpdateMask()
	if mask == nil || len(mask.GetPaths()) == 0 {
		// Full replace semantics: update all provided fields.
		if req.GetName() != "" {
			updated.Name = req.GetName()
		}
		updated.ExternalSchema = req.GetExternalSchema()
		updated.Fields = protoFieldsToCorrespondences(req.GetFields())
		updated.InboundComputed = protoComputedFieldsToDomain(req.GetInboundComputedFields())
		updated.OutboundComputed = protoComputedFieldsToDomain(req.GetOutboundComputedFields())
		updated.InboundValidationCEL = req.GetInboundValidationCel()
		updated.OutboundValidationCEL = req.GetOutboundValidationCel()
		updated.IsBatch = req.GetIsBatch()
		updated.BatchTargetPath = req.GetBatchTargetPath()
		updated.Idempotency = protoIdempotencyToDomain(req.GetIdempotency())
		return &updated
	}

	// FieldMask partial update.
	for _, path := range mask.GetPaths() {
		switch path {
		case "name":
			updated.Name = req.GetName()
		case "external_schema":
			updated.ExternalSchema = req.GetExternalSchema()
		case "fields":
			updated.Fields = protoFieldsToCorrespondences(req.GetFields())
		case "inbound_computed_fields":
			updated.InboundComputed = protoComputedFieldsToDomain(req.GetInboundComputedFields())
		case "outbound_computed_fields":
			updated.OutboundComputed = protoComputedFieldsToDomain(req.GetOutboundComputedFields())
		case "inbound_validation_cel":
			updated.InboundValidationCEL = req.GetInboundValidationCel()
		case "outbound_validation_cel":
			updated.OutboundValidationCEL = req.GetOutboundValidationCel()
		case "is_batch":
			updated.IsBatch = req.GetIsBatch()
		case "batch_target_path":
			updated.BatchTargetPath = req.GetBatchTargetPath()
		case "idempotency":
			updated.Idempotency = protoIdempotencyToDomain(req.GetIdempotency())
		}
	}

	return &updated
}

// mapDomainError converts mapping domain errors to gRPC status codes.
func (s *MappingService) mapDomainError(ctx context.Context, err error, operation, identifier string) error {
	type errorEntry struct {
		sentinel error
		code     codes.Code
		message  string
		logLevel slog.Level
	}

	errorMappings := []errorEntry{
		{tenant.ErrMissingTenantContext, codes.Unauthenticated, "missing tenant context", slog.LevelWarn},
		{mapping.ErrNotFound, codes.NotFound, "mapping definition not found", slog.LevelWarn},
		{mapping.ErrNotDraft, codes.FailedPrecondition, "mapping definition must be in DRAFT status", slog.LevelWarn},
		{mapping.ErrNotActive, codes.FailedPrecondition, "mapping definition must be in ACTIVE status", slog.LevelWarn},
		{mapping.ErrAlreadyExists, codes.AlreadyExists, "mapping definition already exists", slog.LevelWarn},
		{mapping.ErrInvalidCEL, codes.InvalidArgument, "invalid CEL expression", slog.LevelWarn},
		{mapping.ErrInvalidJSONSchema, codes.InvalidArgument, "invalid JSON Schema", slog.LevelWarn},
		{mapping.ErrInvalidGjsonPath, codes.InvalidArgument, "invalid gjson path", slog.LevelWarn},
		{mapping.ErrDuplicateExternalPath, codes.InvalidArgument, "duplicate external_path in fields", slog.LevelWarn},
		{mapping.ErrDuplicateInternalPath, codes.InvalidArgument, "duplicate internal_path in fields", slog.LevelWarn},
		{mapping.ErrBatchTargetPathRequired, codes.InvalidArgument, "batch_target_path required", slog.LevelWarn},
		{mapping.ErrIdempotencyConfig, codes.InvalidArgument, "invalid idempotency config", slog.LevelWarn},
		{mapping.ErrOptimisticLock, codes.Aborted, "mapping definition was modified concurrently", slog.LevelWarn},
		{mapping.ErrInvalidStatusTransition, codes.FailedPrecondition, "invalid status transition", slog.LevelWarn},
	}

	for _, m := range errorMappings {
		if errors.Is(err, m.sentinel) {
			s.logger.Log(ctx, m.logLevel, m.message,
				"operation", operation,
				"identifier", identifier,
				"error", err)
			return status.Errorf(m.code, "%s: %v", m.message, err)
		}
	}

	s.logger.ErrorContext(ctx, "internal error",
		"operation", operation,
		"identifier", identifier,
		"error", err)
	return status.Errorf(codes.Internal, "internal error: %v", err)
}

// --- Proto <-> Domain conversions ---

func mappingToProto(def *mapping.Definition) *pb.MappingDefinition {
	if def == nil {
		return nil
	}

	return &pb.MappingDefinition{
		Id:                     def.ID.String(),
		TenantId:               def.TenantID,
		Name:                   def.Name,
		TargetService:          def.TargetService,
		TargetRpc:              def.TargetRPC,
		Version:                int32(def.Version),
		Status:                 domainStatusToProtoMapping(def.Status),
		ExternalSchema:         def.ExternalSchema,
		Fields:                 correspondencesToProto(def.Fields),
		InboundComputedFields:  computedFieldsToProto(def.InboundComputed),
		OutboundComputedFields: computedFieldsToProto(def.OutboundComputed),
		InboundValidationCel:   def.InboundValidationCEL,
		OutboundValidationCel:  def.OutboundValidationCEL,
		IsBatch:                def.IsBatch,
		BatchTargetPath:        def.BatchTargetPath,
		Idempotency:            domainIdempotencyToProto(def.Idempotency),
		CreatedAt:              timestamppb.New(def.CreatedAt),
		UpdatedAt:              timestamppb.New(def.UpdatedAt),
	}
}

func domainStatusToProtoMapping(s mapping.Status) pb.MappingStatus {
	switch s {
	case mapping.StatusDraft:
		return pb.MappingStatus_MAPPING_STATUS_DRAFT
	case mapping.StatusActive:
		return pb.MappingStatus_MAPPING_STATUS_ACTIVE
	case mapping.StatusDeprecated:
		return pb.MappingStatus_MAPPING_STATUS_DEPRECATED
	default:
		return pb.MappingStatus_MAPPING_STATUS_UNSPECIFIED
	}
}

func protoStatusToDomainMapping(s pb.MappingStatus) mapping.Status {
	switch s {
	case pb.MappingStatus_MAPPING_STATUS_UNSPECIFIED:
		return ""
	case pb.MappingStatus_MAPPING_STATUS_DRAFT:
		return mapping.StatusDraft
	case pb.MappingStatus_MAPPING_STATUS_ACTIVE:
		return mapping.StatusActive
	case pb.MappingStatus_MAPPING_STATUS_DEPRECATED:
		return mapping.StatusDeprecated
	default:
		return ""
	}
}

func protoFieldsToCorrespondences(fields []*pb.FieldCorrespondence) []mapping.FieldCorrespondence {
	if fields == nil {
		return nil
	}
	result := make([]mapping.FieldCorrespondence, len(fields))
	for i, f := range fields {
		result[i] = mapping.FieldCorrespondence{
			ExternalPath: f.GetExternalPath(),
			InternalPath: f.GetInternalPath(),
			Transform:    protoTransformToDomain(f.GetTransform()),
		}
	}
	return result
}

func correspondencesToProto(fields []mapping.FieldCorrespondence) []*pb.FieldCorrespondence {
	if fields == nil {
		return nil
	}
	result := make([]*pb.FieldCorrespondence, len(fields))
	for i, f := range fields {
		result[i] = &pb.FieldCorrespondence{
			ExternalPath: f.ExternalPath,
			InternalPath: f.InternalPath,
			Transform:    domainTransformToProto(f.Transform),
		}
	}
	return result
}

func protoTransformToDomain(t *pb.FieldTransform) *mapping.FieldTransform {
	if t == nil {
		return nil
	}
	ft := &mapping.FieldTransform{}
	switch v := t.GetTransform().(type) {
	case *pb.FieldTransform_Cel:
		ft.CEL = &mapping.CelTransform{
			InboundCEL:  v.Cel.GetInboundCel(),
			OutboundCEL: v.Cel.GetOutboundCel(),
		}
	case *pb.FieldTransform_EnumMapping:
		ft.EnumMapping = &mapping.EnumMapping{
			Values:           v.EnumMapping.GetValues(),
			Fallback:         v.EnumMapping.GetFallback(),
			OutboundFallback: v.EnumMapping.GetOutboundFallback(),
		}
	case *pb.FieldTransform_DateFormat:
		ft.DateFormat = v.DateFormat
	case *pb.FieldTransform_DefaultValue:
		ft.DefaultValue = v.DefaultValue
	case *pb.FieldTransform_AttributeFlatten:
		ft.AttributeFlatten = &mapping.AttributeFlatten{
			SourceKeys:  v.AttributeFlatten.GetSourceKeys(),
			TargetField: v.AttributeFlatten.GetTargetField(),
		}
	}
	return ft
}

func domainTransformToProto(t *mapping.FieldTransform) *pb.FieldTransform {
	if t == nil {
		return nil
	}
	ft := &pb.FieldTransform{}
	switch {
	case t.CEL != nil:
		ft.Transform = &pb.FieldTransform_Cel{
			Cel: &pb.CelTransform{
				InboundCel:  t.CEL.InboundCEL,
				OutboundCel: t.CEL.OutboundCEL,
			},
		}
	case t.EnumMapping != nil:
		ft.Transform = &pb.FieldTransform_EnumMapping{
			EnumMapping: &pb.EnumMapping{
				Values:           t.EnumMapping.Values,
				Fallback:         t.EnumMapping.Fallback,
				OutboundFallback: t.EnumMapping.OutboundFallback,
			},
		}
	case t.DateFormat != "":
		ft.Transform = &pb.FieldTransform_DateFormat{DateFormat: t.DateFormat}
	case t.DefaultValue != "":
		ft.Transform = &pb.FieldTransform_DefaultValue{DefaultValue: t.DefaultValue}
	case t.AttributeFlatten != nil:
		ft.Transform = &pb.FieldTransform_AttributeFlatten{
			AttributeFlatten: &pb.AttributeFlatten{
				SourceKeys:  t.AttributeFlatten.SourceKeys,
				TargetField: t.AttributeFlatten.TargetField,
			},
		}
	}
	return ft
}

func protoComputedFieldsToDomain(fields []*pb.ComputedField) []mapping.ComputedField {
	if fields == nil {
		return nil
	}
	result := make([]mapping.ComputedField, len(fields))
	for i, f := range fields {
		result[i] = mapping.ComputedField{
			TargetPath:    f.GetTargetPath(),
			CELExpression: f.GetCelExpression(),
		}
	}
	return result
}

func computedFieldsToProto(fields []mapping.ComputedField) []*pb.ComputedField {
	if fields == nil {
		return nil
	}
	result := make([]*pb.ComputedField, len(fields))
	for i, f := range fields {
		result[i] = &pb.ComputedField{
			TargetPath:    f.TargetPath,
			CelExpression: f.CELExpression,
		}
	}
	return result
}

func protoIdempotencyToDomain(cfg *pb.IdempotencyConfig) *mapping.IdempotencyConfig {
	if cfg == nil {
		return nil
	}
	return &mapping.IdempotencyConfig{
		SourceSelector:    cfg.GetSourceSelector(),
		UseContentHash:    cfg.GetUseContentHash(),
		ContentHashFields: cfg.GetContentHashFields(),
	}
}

func domainIdempotencyToProto(cfg *mapping.IdempotencyConfig) *pb.IdempotencyConfig {
	if cfg == nil {
		return nil
	}
	return &pb.IdempotencyConfig{
		SourceSelector:    cfg.SourceSelector,
		UseContentHash:    cfg.UseContentHash,
		ContentHashFields: cfg.ContentHashFields,
	}
}
