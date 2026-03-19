//meridian:large-file — known oversized file; split tracked in backlog
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// AccountTypeService implements the AccountTypeRegistryService gRPC service.
type AccountTypeService struct {
	pb.UnimplementedAccountTypeRegistryServiceServer
	registry           accounttype.Registry
	instrumentRegistry registry.InstrumentRegistry
	compiler           *refcel.Compiler
	logger             *slog.Logger
}

// ErrAccountTypeRegistryNil is returned when attempting to create a service with a nil registry.
var ErrAccountTypeRegistryNil = errors.New("account type registry cannot be nil")

// NewAccountTypeService creates a new account type service.
func NewAccountTypeService(
	reg accounttype.Registry,
	instrumentReg registry.InstrumentRegistry,
	compiler *refcel.Compiler,
	logger *slog.Logger,
) (*AccountTypeService, error) {
	if reg == nil {
		return nil, ErrAccountTypeRegistryNil
	}
	if compiler == nil {
		return nil, ErrCompilerNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &AccountTypeService{
		registry:           reg,
		instrumentRegistry: instrumentReg,
		compiler:           compiler,
		logger:             logger,
	}, nil
}

// GetDefinition retrieves a specific account type definition by ID.
func (s *AccountTypeService) GetDefinition(ctx context.Context, req *pb.GetDefinitionRequest) (*pb.GetDefinitionResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	def, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "GetDefinition", req.GetId())
	}

	return &pb.GetDefinitionResponse{
		Definition: accountTypeToProto(def),
	}, nil
}

// GetActiveDefinition retrieves the currently active definition for a given code.
func (s *AccountTypeService) GetActiveDefinition(ctx context.Context, req *pb.GetActiveDefinitionRequest) (*pb.GetActiveDefinitionResponse, error) {
	if req.GetCode() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "code is required")
	}

	def, err := s.registry.GetActiveDefinition(ctx, req.GetCode())
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "GetActiveDefinition", req.GetCode())
	}

	return &pb.GetActiveDefinitionResponse{
		Definition: accountTypeToProto(def),
	}, nil
}

// ListActive returns all active account type definitions.
func (s *AccountTypeService) ListActive(ctx context.Context, req *pb.ListActiveRequest) (*pb.ListActiveResponse, error) {
	defs, err := s.registry.ListActive(ctx)
	if err != nil {
		s.logger.Error("failed to list active account types", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list active account types: %v", err)
	}

	defs = filterByBehaviorClass(defs, req.GetBehaviorClassFilter())
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Code < defs[j].Code
	})

	page, nextPageToken := paginateDefinitions(defs, int(req.GetPageSize()), req.GetPageToken())

	definitions := make([]*pb.AccountTypeDefinition, len(page))
	for i, def := range page {
		definitions[i] = accountTypeToProto(def)
	}

	return &pb.ListActiveResponse{
		Definitions:   definitions,
		NextPageToken: nextPageToken,
	}, nil
}

func filterByBehaviorClass(defs []*accounttype.Definition, filter pb.BehaviorClass) []*accounttype.Definition {
	if filter == pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED {
		return defs
	}
	domainBC := protoBehaviorClassToDomain(filter)
	var filtered []*accounttype.Definition
	for _, def := range defs {
		if def.BehaviorClass == domainBC {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

func paginateDefinitions(defs []*accounttype.Definition, reqPageSize int, pageToken string) ([]*accounttype.Definition, string) {
	pageSize := normalizeAccountTypePageSize(reqPageSize)
	startIdx := findStartIndex(defs, pageToken)

	if startIdx >= len(defs) {
		return nil, ""
	}

	end := startIdx + pageSize
	if end > len(defs) {
		end = len(defs)
	}
	page := defs[startIdx:end]

	var nextPageToken string
	if end < len(defs) {
		nextPageToken = page[len(page)-1].Code
	}
	return page, nextPageToken
}

func findStartIndex(defs []*accounttype.Definition, pageToken string) int {
	if pageToken == "" {
		return 0
	}
	for i, def := range defs {
		if def.Code > pageToken {
			return i
		}
	}
	return len(defs)
}

func normalizeAccountTypePageSize(pageSize int) int {
	if pageSize <= 0 {
		return DefaultPageSize
	}
	if pageSize > MaxPageSize {
		return MaxPageSize
	}
	return pageSize
}

// CreateDraft creates a new account type definition in DRAFT status.
func (s *AccountTypeService) CreateDraft(ctx context.Context, req *pb.CreateDraftRequest) (*pb.CreateDraftResponse, error) {
	defaultConversionMethodID, defaultConversionMethodVersion, err := parseConversionMethodPair(
		req.GetDefaultConversionMethodId(), req.GetDefaultConversionMethodVersion())
	if err != nil {
		return nil, err
	}

	def, err := accounttype.NewDefinition(accounttype.NewDefinitionParams{
		Code:                           req.GetCode(),
		DisplayName:                    req.GetDisplayName(),
		Description:                    req.GetDescription(),
		NormalBalance:                  protoBehaviorNormalBalanceToDomainString(req.GetNormalBalance()),
		BehaviorClass:                  protoBehaviorClassToDomainString(req.GetBehaviorClass()),
		InstrumentCode:                 req.GetInstrumentCode(),
		DefaultSagaPrefix:              req.GetDefaultSagaPrefix(),
		DefaultConversionMethodID:      defaultConversionMethodID,
		DefaultConversionMethodVersion: defaultConversionMethodVersion,
		ValidationCEL:                  req.GetValidationCel(),
		BucketingCEL:                   req.GetBucketingCel(),
		EligibilityCEL:                 req.GetEligibilityCel(),
		AttributeSchema:                toRawJSON(req.GetAttributeSchema()),
		Attributes:                     stringMapToAnyMap(req.GetAttributes()),
		Compiler:                       s.compiler,
	})
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "CreateDraft", req.GetCode())
	}

	if err := s.registry.CreateDraft(ctx, def); err != nil {
		return nil, s.mapDomainError(ctx, err, "CreateDraft", req.GetCode())
	}

	s.logger.Info("account type draft created",
		"code", def.Code,
		"version", def.Version)

	return &pb.CreateDraftResponse{
		Definition: accountTypeToProto(def),
	}, nil
}

// UpdateDefinition modifies a DRAFT account type definition.
func (s *AccountTypeService) UpdateDefinition(ctx context.Context, req *pb.UpdateDefinitionRequest) (*pb.UpdateDefinitionResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	existing, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateDefinition", req.GetId())
	}

	defaultConversionMethodID, defaultConversionMethodVersion, parseErr := parseConversionMethodPair(
		req.GetDefaultConversionMethodId(), req.GetDefaultConversionMethodVersion())
	if parseErr != nil {
		return nil, parseErr
	}

	updates := &accounttype.Definition{
		DisplayName:                    req.GetDisplayName(),
		Description:                    req.GetDescription(),
		InstrumentCode:                 req.GetInstrumentCode(),
		DefaultSagaPrefix:              req.GetDefaultSagaPrefix(),
		DefaultConversionMethodID:      defaultConversionMethodID,
		DefaultConversionMethodVersion: defaultConversionMethodVersion,
		ValidationCEL:                  req.GetValidationCel(),
		BucketingCEL:                   req.GetBucketingCel(),
		EligibilityCEL:                 req.GetEligibilityCel(),
		AttributeSchema:                toRawJSON(req.GetAttributeSchema()),
		Attributes:                     stringMapToAnyMap(req.GetAttributes()),
	}

	if err := s.registry.UpdateDefinition(ctx, existing.Code, existing.Version, updates); err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateDefinition", req.GetId())
	}

	updated, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "UpdateDefinition", req.GetId())
	}

	s.logger.Info("account type updated",
		"code", existing.Code,
		"version", existing.Version)

	return &pb.UpdateDefinitionResponse{
		Definition: accountTypeToProto(updated),
	}, nil
}

// ActivateAccountType transitions an account type from DRAFT to ACTIVE.
func (s *AccountTypeService) ActivateAccountType(ctx context.Context, req *pb.ActivateAccountTypeRequest) (*pb.ActivateAccountTypeResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	existing, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "ActivateAccountType", req.GetId())
	}

	if err := s.registry.ActivateAccountType(ctx, existing.Code, existing.Version); err != nil {
		return nil, s.mapDomainError(ctx, err, "ActivateAccountType", req.GetId())
	}

	activated, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "ActivateAccountType", req.GetId())
	}

	s.logger.Info("account type activated",
		"code", existing.Code,
		"version", existing.Version)

	return &pb.ActivateAccountTypeResponse{
		Definition: accountTypeToProto(activated),
	}, nil
}

// DeprecateAccountType transitions an account type from ACTIVE to DEPRECATED.
func (s *AccountTypeService) DeprecateAccountType(ctx context.Context, req *pb.DeprecateAccountTypeRequest) (*pb.DeprecateAccountTypeResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	existing, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "DeprecateAccountType", req.GetId())
	}

	var successorID *uuid.UUID
	if req.GetSuccessorId() != "" {
		parsed, parseErr := uuid.Parse(req.GetSuccessorId())
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid successor_id: %v", parseErr)
		}
		successorID = &parsed
	}

	if err := s.registry.DeprecateAccountType(ctx, existing.Code, existing.Version, successorID); err != nil {
		return nil, s.mapDomainError(ctx, err, "DeprecateAccountType", req.GetId())
	}

	deprecated, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "DeprecateAccountType", req.GetId())
	}

	s.logger.Info("account type deprecated",
		"code", existing.Code,
		"version", existing.Version)

	return &pb.DeprecateAccountTypeResponse{
		Definition: accountTypeToProto(deprecated),
	}, nil
}

// ValidateProductDefinition validates an account type definition without persisting it.
func (s *AccountTypeService) ValidateProductDefinition(ctx context.Context, req *pb.ValidateProductDefinitionRequest) (*pb.ValidateProductDefinitionResponse, error) {
	def := req.GetDefinition()
	if def == nil {
		return nil, status.Errorf(codes.InvalidArgument, "definition is required")
	}

	validationErrors := make([]*pb.ValidationError, 0, 4)

	validationErrors = append(validationErrors, s.validateCELExpressions(def)...)
	schemaValid, schemaErrs := validateAttributeSchema(def.AttributeSchema)
	validationErrors = append(validationErrors, schemaErrs...)
	validationErrors = append(validationErrors, validateAttrsAgainstSchema(def, schemaValid)...)
	validationErrors = append(validationErrors, s.validateInstrumentRefs(ctx, def)...)

	return &pb.ValidateProductDefinitionResponse{
		Valid:  len(validationErrors) == 0,
		Errors: validationErrors,
	}, nil
}

func (s *AccountTypeService) validateCELExpressions(def *pb.AccountTypeDefinition) []*pb.ValidationError {
	var errs []*pb.ValidationError
	if def.ValidationCel != "" {
		if err := s.compiler.ValidateValidationCEL(def.ValidationCel); err != nil {
			errs = append(errs, celCompileError("validation_cel", err))
		}
	}
	if def.BucketingCel != "" {
		if err := s.compiler.ValidateBucketingCEL(def.BucketingCel); err != nil {
			errs = append(errs, celCompileError("bucketing_cel", err))
		}
	}
	if def.EligibilityCel != "" {
		if err := s.compiler.ValidateEligibilityCEL(def.EligibilityCel); err != nil {
			errs = append(errs, celCompileError("eligibility_cel", err))
		}
	}
	return errs
}

func validateAttributeSchema(schema string) (bool, []*pb.ValidationError) {
	if schema == "" {
		return false, nil
	}
	if !json.Valid([]byte(schema)) {
		return false, []*pb.ValidationError{{
			Field:     "attribute_schema",
			ErrorCode: "INVALID_JSON",
			Message:   "attribute_schema is not valid JSON",
		}}
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", strings.NewReader(schema)); err != nil {
		return false, []*pb.ValidationError{{
			Field:     "attribute_schema",
			ErrorCode: "INVALID_JSON_SCHEMA",
			Message:   fmt.Sprintf("invalid JSON Schema: %v", err),
		}}
	}
	if _, err := c.Compile("schema.json"); err != nil {
		return false, []*pb.ValidationError{{
			Field:     "attribute_schema",
			ErrorCode: "INVALID_JSON_SCHEMA",
			Message:   fmt.Sprintf("JSON Schema compilation failed: %v", err),
		}}
	}
	return true, nil
}

func validateAttrsAgainstSchema(def *pb.AccountTypeDefinition, schemaValid bool) []*pb.ValidationError {
	if !schemaValid || len(def.Attributes) == 0 {
		return nil
	}
	attrs := stringMapToAnyMap(def.Attributes)
	c := jsonschema.NewCompiler()
	_ = c.AddResource("schema.json", strings.NewReader(def.AttributeSchema))
	compiled, _ := c.Compile("schema.json")
	if err := compiled.Validate(attrs); err != nil {
		return []*pb.ValidationError{{
			Field:     "attributes",
			ErrorCode: "SCHEMA_VALIDATION_FAILED",
			Message:   fmt.Sprintf("attributes do not validate against schema: %v", err),
		}}
	}
	return nil
}

func (s *AccountTypeService) validateInstrumentRefs(ctx context.Context, def *pb.AccountTypeDefinition) []*pb.ValidationError {
	if s.instrumentRegistry == nil {
		return nil
	}
	var errs []*pb.ValidationError

	if def.InstrumentCode != "" {
		if _, err := s.instrumentRegistry.GetActiveDefinition(ctx, def.InstrumentCode); err != nil {
			ve := &pb.ValidationError{
				Field:       "instrument_code",
				ErrorCode:   "UNRESOLVABLE_REFERENCE",
				Message:     fmt.Sprintf("instrument %q not found or not ACTIVE", def.InstrumentCode),
				Suggestions: s.instrumentSuggestions(ctx, def.InstrumentCode),
			}
			errs = append(errs, ve)
		}
	}

	for i, vmt := range def.ValuationMethods {
		if vmt.InputInstrument != "" {
			if _, err := s.instrumentRegistry.GetActiveDefinition(ctx, vmt.InputInstrument); err != nil {
				ve := &pb.ValidationError{
					Field:       fmt.Sprintf("valuation_methods[%d].input_instrument", i),
					ErrorCode:   "UNRESOLVABLE_REFERENCE",
					Message:     fmt.Sprintf("instrument %q not found or not ACTIVE", vmt.InputInstrument),
					Suggestions: s.instrumentSuggestions(ctx, vmt.InputInstrument),
				}
				errs = append(errs, ve)
			}
		}
	}

	return errs
}

// instrumentSuggestions returns Levenshtein-based suggestions for a typo instrument code.
func (s *AccountTypeService) instrumentSuggestions(ctx context.Context, code string) []string {
	if s.instrumentRegistry == nil {
		return nil
	}
	actives, err := s.instrumentRegistry.ListActive(ctx)
	if err != nil {
		return nil
	}
	candidates := make([]string, len(actives))
	for i, a := range actives {
		candidates[i] = a.Code
	}
	return findSuggestions(code, candidates)
}

func celCompileError(field string, err error) *pb.ValidationError {
	return &pb.ValidationError{
		Field:     field,
		ErrorCode: "CEL_COMPILE_ERROR",
		Message:   err.Error(),
	}
}

// accountTypeErrorMapping maps domain errors to gRPC status codes.
var accountTypeErrorMapping = []struct {
	sentinel error
	code     codes.Code
	message  string
	logLevel slog.Level
}{
	{accounttype.ErrNotFound, codes.NotFound, "account type not found", slog.LevelWarn},
	{accounttype.ErrOptimisticLock, codes.Aborted, "account type was modified by another transaction", slog.LevelWarn},
	{accounttype.ErrNotDraft, codes.FailedPrecondition, "account type must be in DRAFT status", slog.LevelWarn},
	{accounttype.ErrNotActive, codes.FailedPrecondition, "account type must be in ACTIVE status", slog.LevelWarn},
	{accounttype.ErrFieldImmutable, codes.InvalidArgument, "immutable field", slog.LevelWarn},
	{accounttype.ErrInvalidCEL, codes.InvalidArgument, "invalid CEL expression", slog.LevelWarn},
	{accounttype.ErrActiveCodeExists, codes.AlreadyExists, "active account type already exists", slog.LevelWarn},
	{accounttype.ErrInvalidBehaviorClass, codes.InvalidArgument, "invalid behavior class", slog.LevelWarn},
	{accounttype.ErrInvalidNormalBalance, codes.InvalidArgument, "invalid normal balance", slog.LevelWarn},
	{accounttype.ErrConversionMethodPair, codes.InvalidArgument, "default_conversion_method_id and default_conversion_method_version must both be set or both be empty", slog.LevelWarn},
	{accounttype.ErrSuccessorWriteOnce, codes.FailedPrecondition, "successor_id is write-once and already set", slog.LevelWarn},
	{accounttype.ErrInvalidInstrument, codes.FailedPrecondition, "instrument validation failed", slog.LevelWarn},
	{accounttype.ErrInvalidConversionMethod, codes.FailedPrecondition, "conversion method validation failed", slog.LevelWarn},
	{accounttype.ErrInvalidValuationMethod, codes.FailedPrecondition, "valuation method validation failed", slog.LevelWarn},
	{accounttype.ErrInvalidAttributeSchema, codes.InvalidArgument, "invalid attribute schema", slog.LevelWarn},
	{accounttype.ErrAttributesInvalid, codes.InvalidArgument, "attributes validation failed", slog.LevelWarn},
}

// mapDomainError converts accounttype domain errors to gRPC status codes.
func (s *AccountTypeService) mapDomainError(ctx context.Context, err error, operation, identifier string) error {
	for _, m := range accountTypeErrorMapping {
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

// --- Proto <-> Domain mapping ---

func accountTypeToProto(def *accounttype.Definition) *pb.AccountTypeDefinition {
	if def == nil {
		return nil
	}

	proto := &pb.AccountTypeDefinition{
		Id:                def.ID.String(),
		Code:              def.Code,
		Version:           int32(def.Version),
		DisplayName:       def.DisplayName,
		Description:       def.Description,
		NormalBalance:     domainNormalBalanceToProto(def.NormalBalance),
		BehaviorClass:     domainBehaviorClassToProto(def.BehaviorClass),
		InstrumentCode:    def.InstrumentCode,
		DefaultSagaPrefix: def.DefaultSagaPrefix,
		ValidationCel:     def.ValidationCEL,
		BucketingCel:      def.BucketingCEL,
		EligibilityCel:    def.EligibilityCEL,
		AttributeSchema:   string(def.AttributeSchema),
		Attributes:        anyMapToStringMap(def.Attributes),
		Status:            domainAccountTypeStatusToProto(def.Status),
		IsSystem:          def.IsSystem,
		CreatedAt:         timestamppb.New(def.CreatedAt),
		UpdatedAt:         timestamppb.New(def.UpdatedAt),
	}

	if def.DefaultConversionMethodID != nil {
		proto.DefaultConversionMethodId = def.DefaultConversionMethodID.String()
	}
	if def.DefaultConversionMethodVersion != nil {
		proto.DefaultConversionMethodVersion = int32(*def.DefaultConversionMethodVersion)
	}
	if def.SuccessorID != nil {
		proto.SuccessorId = def.SuccessorID.String()
	}
	if def.ActivatedAt != nil {
		proto.ActivatedAt = timestamppb.New(*def.ActivatedAt)
	}
	if def.DeprecatedAt != nil {
		proto.DeprecatedAt = timestamppb.New(*def.DeprecatedAt)
	}

	if len(def.ValuationMethods) > 0 {
		proto.ValuationMethods = make([]*pb.ValuationMethodTemplate, len(def.ValuationMethods))
		for i, vmt := range def.ValuationMethods {
			proto.ValuationMethods[i] = valuationMethodToProto(&vmt)
		}
	}

	return proto
}

func valuationMethodToProto(vmt *accounttype.ValuationMethodTemplate) *pb.ValuationMethodTemplate {
	proto := &pb.ValuationMethodTemplate{
		Id:                     vmt.ID.String(),
		AccountTypeId:          vmt.AccountTypeID.String(),
		InputInstrument:        vmt.InputInstrument,
		ValuationMethodId:      vmt.ValuationMethodID.String(),
		ValuationMethodVersion: int32(vmt.ValuationMethodVersion),
		Parameters:             anyMapToStringMap(vmt.Parameters),
		Status:                 domainAccountTypeStatusToProto(vmt.Status),
	}
	if vmt.SuccessorID != nil {
		proto.SuccessorId = vmt.SuccessorID.String()
	}
	return proto
}

// --- Enum mappings ---

func domainAccountTypeStatusToProto(s accounttype.Status) pb.AccountTypeStatus {
	switch s {
	case accounttype.StatusDraft:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DRAFT
	case accounttype.StatusActive:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE
	case accounttype.StatusDeprecated:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DEPRECATED
	default:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_UNSPECIFIED
	}
}

func domainNormalBalanceToProto(n accounttype.NormalBalance) pb.NormalBalance {
	switch n {
	case accounttype.NormalBalanceDebit:
		return pb.NormalBalance_NORMAL_BALANCE_DEBIT
	case accounttype.NormalBalanceCredit:
		return pb.NormalBalance_NORMAL_BALANCE_CREDIT
	default:
		return pb.NormalBalance_NORMAL_BALANCE_UNSPECIFIED
	}
}

func domainBehaviorClassToProto(b accounttype.BehaviorClass) pb.BehaviorClass {
	switch b {
	case accounttype.BehaviorClassCustomer:
		return pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER
	case accounttype.BehaviorClassClearing:
		return pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING
	case accounttype.BehaviorClassNostro:
		return pb.BehaviorClass_BEHAVIOR_CLASS_NOSTRO
	case accounttype.BehaviorClassVostro:
		return pb.BehaviorClass_BEHAVIOR_CLASS_VOSTRO
	case accounttype.BehaviorClassHolding:
		return pb.BehaviorClass_BEHAVIOR_CLASS_HOLDING
	case accounttype.BehaviorClassSuspense:
		return pb.BehaviorClass_BEHAVIOR_CLASS_SUSPENSE
	case accounttype.BehaviorClassRevenue:
		return pb.BehaviorClass_BEHAVIOR_CLASS_REVENUE
	case accounttype.BehaviorClassExpense:
		return pb.BehaviorClass_BEHAVIOR_CLASS_EXPENSE
	case accounttype.BehaviorClassInventory:
		return pb.BehaviorClass_BEHAVIOR_CLASS_INVENTORY
	default:
		return pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED
	}
}

func protoBehaviorClassToDomain(b pb.BehaviorClass) accounttype.BehaviorClass {
	switch b {
	case pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED:
		return ""
	case pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER:
		return accounttype.BehaviorClassCustomer
	case pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING:
		return accounttype.BehaviorClassClearing
	case pb.BehaviorClass_BEHAVIOR_CLASS_NOSTRO:
		return accounttype.BehaviorClassNostro
	case pb.BehaviorClass_BEHAVIOR_CLASS_VOSTRO:
		return accounttype.BehaviorClassVostro
	case pb.BehaviorClass_BEHAVIOR_CLASS_HOLDING:
		return accounttype.BehaviorClassHolding
	case pb.BehaviorClass_BEHAVIOR_CLASS_SUSPENSE:
		return accounttype.BehaviorClassSuspense
	case pb.BehaviorClass_BEHAVIOR_CLASS_REVENUE:
		return accounttype.BehaviorClassRevenue
	case pb.BehaviorClass_BEHAVIOR_CLASS_EXPENSE:
		return accounttype.BehaviorClassExpense
	case pb.BehaviorClass_BEHAVIOR_CLASS_INVENTORY:
		return accounttype.BehaviorClassInventory
	default:
		return ""
	}
}

func protoBehaviorClassToDomainString(b pb.BehaviorClass) string {
	return string(protoBehaviorClassToDomain(b))
}

func protoBehaviorNormalBalanceToDomainString(n pb.NormalBalance) string {
	switch n {
	case pb.NormalBalance_NORMAL_BALANCE_UNSPECIFIED:
		return ""
	case pb.NormalBalance_NORMAL_BALANCE_DEBIT:
		return "DEBIT"
	case pb.NormalBalance_NORMAL_BALANCE_CREDIT:
		return "CREDIT"
	default:
		return ""
	}
}

// --- Map utilities ---

// parseConversionMethodPair validates and parses the conversion method ID/version pair.
// Returns nil pointers if ID is empty. Returns InvalidArgument if ID is invalid or version < 1.
func parseConversionMethodPair(idStr string, version int32) (*uuid.UUID, *int, error) {
	if idStr == "" {
		return nil, nil, nil
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "invalid default_conversion_method_id: %v", err)
	}
	if version < 1 {
		return nil, nil, status.Errorf(codes.InvalidArgument, "default_conversion_method_version must be >= 1 when default_conversion_method_id is set")
	}
	v := int(version)
	return &id, &v, nil
}

// toRawJSON converts a proto string field to json.RawMessage, returning nil for
// empty strings so that PostgreSQL jsonb columns receive NULL rather than
// invalid empty-string JSON.
func toRawJSON(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func stringMapToAnyMap(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func anyMapToStringMap(m map[string]any) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

// findSuggestions returns up to 3 closest matches by Levenshtein distance (max distance 3).
func findSuggestions(query string, candidates []string) []string {
	type scored struct {
		value    string
		distance int
	}

	var matches []scored
	for _, c := range candidates {
		d := levenshtein(strings.ToUpper(query), strings.ToUpper(c))
		if d <= 3 && d > 0 {
			matches = append(matches, scored{value: c, distance: d})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].distance < matches[j].distance
	})

	limit := 3
	if len(matches) < limit {
		limit = len(matches)
	}

	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = matches[i].value
	}
	return result
}
