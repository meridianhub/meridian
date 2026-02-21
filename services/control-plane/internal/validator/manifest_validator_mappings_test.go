package validator

import (
	"strings"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
)

// validMapping returns a fully valid MappingDefinition for testing.
// Includes required id and tenant_id UUIDs to satisfy proto validation.
func validMapping() *mappingv1.MappingDefinition {
	return &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000002",
		Name:          "stripe_webhook",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
	}
}

func TestValidateMappings_ValidSingleMapping(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Mappings = []*mappingv1.MappingDefinition{validMapping()}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with mapping, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_NoMappings(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// No mappings - should still be valid
	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest without mappings, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_DuplicateNameVersion(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	mp2 := validMapping()
	mp2.Id = "00000000-0000-0000-0000-000000000003" // Different ID, same (name, version)

	m := validManifest()
	m.Mappings = []*mappingv1.MappingDefinition{
		validMapping(),
		mp2, // Duplicate (name="stripe_webhook", version=1)
	}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for duplicate mapping (name, version)")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_MAPPING" {
			found = true
			if !strings.Contains(e.Message, "stripe_webhook") {
				t.Errorf("expected message to mention name, got: %s", e.Message)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected DUPLICATE_MAPPING error, got: %v", result.Errors)
	}
}

func TestValidateMappings_SameNameDifferentVersion_Valid(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp2 := validMapping()
	mp2.Id = "00000000-0000-0000-0000-000000000003"
	mp2.Version = 2
	m.Mappings = []*mappingv1.MappingDefinition{
		validMapping(),
		mp2, // Same name, different version - this is valid
	}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with same name but different versions, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_UnspecifiedStatus(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.Status = mappingv1.MappingStatus_MAPPING_STATUS_UNSPECIFIED
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for unspecified mapping status")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_MAPPING_STATUS" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected INVALID_MAPPING_STATUS error, got: %v", result.Errors)
	}
}

func TestValidateMappings_DraftStatus_Valid(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.Status = mappingv1.MappingStatus_MAPPING_STATUS_DRAFT
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with DRAFT status, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_DeprecatedStatus_Valid(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.Status = mappingv1.MappingStatus_MAPPING_STATUS_DEPRECATED
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with DEPRECATED status, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_BatchTrueWithoutPath(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.IsBatch = true
	mp.BatchTargetPath = "" // Missing required path
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for batch=true without batch_target_path")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "MAPPING_BATCH_TARGET_REQUIRED" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected MAPPING_BATCH_TARGET_REQUIRED error, got: %v", result.Errors)
	}
}

func TestValidateMappings_BatchTrueWithPath_Valid(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.IsBatch = true
	mp.BatchTargetPath = "data.events"
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest for batch=true with batch_target_path, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_ValidInboundCEL(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.InboundValidationCel = "payload.size() > 0"
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with inbound CEL, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_InvalidInboundCEL(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.InboundValidationCel = "invalid $$$ expression"
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for bad inbound CEL expression")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "CEL_COMPILATION_ERROR" && strings.Contains(e.Path, "inbound_validation_cel") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CEL_COMPILATION_ERROR for inbound_validation_cel, got: %v", result.Errors)
	}
}

func TestValidateMappings_InvalidOutboundCEL(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.OutboundValidationCel = "invalid @@@ expression"
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for bad outbound CEL expression")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "CEL_COMPILATION_ERROR" && strings.Contains(e.Path, "outbound_validation_cel") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CEL_COMPILATION_ERROR for outbound_validation_cel, got: %v", result.Errors)
	}
}

func TestValidateMappings_EmptyCELExpressions_Valid(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.InboundValidationCel = ""
	mp.OutboundValidationCel = ""
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with empty CEL expressions, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_WithFields_ValidCELTransform(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.Fields = []*mappingv1.FieldCorrespondence{
		{
			ExternalPath: "customer.name",
			InternalPath: "customer_name",
			Transform: &mappingv1.FieldTransform{
				Transform: &mappingv1.FieldTransform_Cel{
					Cel: &mappingv1.CelTransform{
						// Use simple expressions that work in standard CEL
						InboundCel:  "string(value)",
						OutboundCel: "string(value)",
					},
				},
			},
		},
	}
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with CEL field transform, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_MultipleErrors(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.Status = mappingv1.MappingStatus_MAPPING_STATUS_UNSPECIFIED
	mp.IsBatch = true
	mp.BatchTargetPath = ""
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest with multiple mapping errors")
	}
	if len(result.Errors) < 2 {
		t.Errorf("expected at least 2 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestValidateMappings_ValidManifestUnchangedByMappings(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Existing valid manifest without mappings should still pass
	m := validManifest()
	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest unchanged by mapping validation, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_MappingWithComputedFields_Valid(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	mp := validMapping()
	mp.InboundComputedFields = []*mappingv1.ComputedField{
		{
			TargetPath:    "created_at",
			CelExpression: "payload.size() > 0",
		},
	}
	m.Mappings = []*mappingv1.MappingDefinition{mp}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with computed fields, got errors: %v", result.Errors)
	}
}

func TestValidateMappings_CompatibilityWithExistingManifest(t *testing.T) {
	// Verify existing manifest test helper still works with the new mapping section
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Mappings = []*mappingv1.MappingDefinition{
		{
			Id:             "00000000-0000-0000-0000-000000000001",
			TenantId:       "00000000-0000-0000-0000-000000000002",
			Name:           "webhook_v1",
			TargetService:  "meridian.payment_order.v1.PaymentOrderService",
			TargetRpc:      "InitiatePaymentOrder",
			Version:        1,
			Status:         mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
			ExternalSchema: `{"type": "object", "properties": {"amount": {"type": "number"}}}`,
		},
	}

	result := v.Validate(m, nil)
	// ExternalSchema is just a string; validation doesn't parse it
	if !result.Valid {
		// Check if errors are from mapping section
		for _, e := range result.Errors {
			if strings.Contains(e.Path, "mappings") {
				t.Errorf("unexpected mapping validation error: %v", e)
			}
		}
	}
}

// validManifestWithMapping returns a manifest with a single valid mapping.
func validManifestWithMapping() *controlplanev1.Manifest {
	m := validManifest()
	m.Mappings = []*mappingv1.MappingDefinition{validMapping()}
	return m
}

func TestValidateMappings_FullRoundTrip(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifestWithMapping()
	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid full manifest with mapping, got errors: %v", result.Errors)
	}
	if len(result.Warnings) > 0 {
		t.Logf("warnings (not failures): %v", result.Warnings)
	}
}
