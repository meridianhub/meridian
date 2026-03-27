package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
)

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
