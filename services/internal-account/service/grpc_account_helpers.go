package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// resolveProductType resolves the product type from cache, validates behavior class,
// evaluates eligibility CEL, and validates attributes against the schema.
func (s *Service) resolveProductType(ctx context.Context, req *pb.InitiateInternalAccountRequest) (domain.AccountType, int, *accounttype.Definition, error) {
	productTypeCode := req.ProductTypeCode

	if productTypeCode == "" {
		return "", 0, nil, status.Error(codes.InvalidArgument,
			"product_type_code is required; the deprecated account_type enum has been removed")
	}

	if s.accountTypeCache == nil {
		return "", 0, nil, status.Error(codes.FailedPrecondition,
			"product type resolution not available; configure account type cache")
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return "", 0, nil, status.Error(codes.InvalidArgument, "tenant context required")
	}

	cached, err := s.accountTypeCache.GetOrLoad(ctx, tenantID, productTypeCode)
	if err != nil {
		s.logger.Warn("product type resolution failed",
			"product_type_code", productTypeCode, "error", err)
		return "", 0, nil, status.Errorf(codes.InvalidArgument, "product type not found: %s", productTypeCode)
	}

	if cached.Definition == nil {
		return "", 0, nil, status.Errorf(codes.Internal, "product type %s has no definition", productTypeCode)
	}

	def := cached.Definition

	if !internalBehaviorClasses[def.BehaviorClass] {
		return "", 0, nil, status.Errorf(codes.InvalidArgument,
			"product type %s has behavior class %s which is not an internal account type",
			productTypeCode, def.BehaviorClass)
	}

	if err := evaluateEligibility(cached, def, req); err != nil {
		return "", 0, nil, err
	}

	if err := validateProductTypeAttributes(cached, req); err != nil {
		return "", 0, nil, err
	}

	accountType := behaviorClassToAccountType[def.BehaviorClass]
	return accountType, def.Version, def, nil
}

// evaluateEligibility runs the CEL eligibility program if configured.
func evaluateEligibility(cached *cache.CachedAccountType, def *accounttype.Definition, req *pb.InitiateInternalAccountRequest) error {
	if cached.EligibilityProgram == nil || def.EligibilityCEL == "" || def.EligibilityCEL == "true" {
		return nil
	}
	activation := map[string]interface{}{
		"instrument_code": req.InstrumentCode,
		"account_code":    req.AccountCode,
	}
	out, _, evalErr := cached.EligibilityProgram.Eval(activation)
	if evalErr != nil {
		return status.Errorf(codes.Internal, "eligibility evaluation failed: %v", evalErr)
	}
	eligible, isBool := out.Value().(bool)
	if !isBool || !eligible {
		return status.Errorf(codes.FailedPrecondition,
			"account not eligible per product type %s eligibility rules", req.ProductTypeCode)
	}
	return nil
}

// validateProductTypeAttributes validates request attributes against the product type's JSON schema.
func validateProductTypeAttributes(cached *cache.CachedAccountType, req *pb.InitiateInternalAccountRequest) error {
	if cached.CompiledSchema == nil {
		return nil
	}
	attrsMap := map[string]interface{}{}
	if req.Attributes != nil {
		attrsMap = req.Attributes.AsMap()
	}
	attrsJSON, err := json.Marshal(attrsMap)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid attributes: %v", err)
	}
	var attrs interface{}
	if err := json.Unmarshal(attrsJSON, &attrs); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid attributes JSON: %v", err)
	}
	if err := cached.CompiledSchema.Validate(attrs); err != nil {
		return status.Errorf(codes.InvalidArgument, "attributes validation failed: %v", err)
	}
	return nil
}

// validateInstrument checks that an instrument exists and is ACTIVE via Reference Data service.
// Returns the instrument dimension, operation status on error, and any error.
func (s *Service) validateInstrument(ctx context.Context, instrumentCode string) (string, string, error) {
	if s.referenceDataClient == nil {
		return "", "", nil
	}

	validationStart := time.Now()
	refDataCtx, refDataCancel := context.WithTimeout(ctx, 5*time.Second)
	defer refDataCancel()

	refDataResp, err := s.referenceDataClient.RetrieveInstrument(refDataCtx, &referencedatav1.RetrieveInstrumentRequest{
		Code: instrumentCode,
	})
	if err != nil {
		return "", "", s.mapInstrumentValidationError(err, instrumentCode, validationStart)
	}

	if refDataResp.Instrument == nil {
		validationDuration := time.Since(validationStart)
		s.logger.Error("reference data service returned nil instrument", "instrument_code", instrumentCode)
		ibaobservability.RecordInstrumentValidation("error", validationDuration)
		return "", opStatusInstrumentValidationErr,
			status.Errorf(codes.Internal, "reference data service returned invalid response for instrument: %s", instrumentCode)
	}

	if refDataResp.Instrument.Status != referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
		validationDuration := time.Since(validationStart)
		s.logger.Warn("instrument not active",
			"instrument_code", instrumentCode, "status", refDataResp.Instrument.Status.String())
		ibaobservability.RecordInstrumentValidation("not_active", validationDuration)
		return "", opStatusInstrumentNotActive,
			status.Errorf(codes.InvalidArgument, "instrument %s is not active (status: %s)",
				instrumentCode, refDataResp.Instrument.Status.String())
	}

	dimension := strings.TrimPrefix(refDataResp.Instrument.Dimension.String(), "DIMENSION_")
	ibaobservability.RecordInstrumentValidation("success", time.Since(validationStart))
	return dimension, "", nil
}

// mapInstrumentValidationError maps a Reference Data retrieval error to a gRPC status error.
func (s *Service) mapInstrumentValidationError(err error, instrumentCode string, validationStart time.Time) error {
	validationDuration := time.Since(validationStart)
	errCode := status.Code(err)
	s.logger.Warn("instrument validation failed", "instrument_code", instrumentCode, "error", err)

	switch errCode {
	case codes.NotFound:
		ibaobservability.RecordInstrumentValidation("not_found", validationDuration)
		return status.Errorf(codes.InvalidArgument, "instrument not found: %s", instrumentCode)
	case codes.DeadlineExceeded, codes.Canceled:
		ibaobservability.RecordInstrumentValidation("timeout", validationDuration)
		return status.Errorf(codes.DeadlineExceeded, "instrument validation timed out for: %s", instrumentCode)
	default:
		ibaobservability.RecordInstrumentValidation("error", validationDuration)
		return status.Errorf(codes.Internal, "failed to validate instrument: %v", err)
	}
}
