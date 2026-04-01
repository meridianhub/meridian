package differ

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMapping returns a sample MappingDefinition for testing.
func testMapping(name string, version int32) *mappingv1.MappingDefinition {
	return &mappingv1.MappingDefinition{
		Name:          name,
		Version:       version,
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
	}
}

// testManifestWithMappings returns a manifest containing a single mapping.
func testManifestWithMappings() *controlplanev1.Manifest {
	m := testManifest()
	m.Mappings = []*mappingv1.MappingDefinition{
		testMapping("stripe_webhook", 1),
	}
	return m
}

func TestDiff_MappingAdded_Create(t *testing.T) {
	d := New(nil, nil, nil)
	oldManifest := testManifest()
	newManifest := testManifestWithMappings()

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceMapping)
	assert.Len(t, creates, 1)
	assert.Equal(t, "stripe_webhook:1", creates[0].ResourceCode)
	assert.Equal(t, ResourceMapping, creates[0].ResourceType)
	assert.Contains(t, creates[0].Description, "stripe_webhook")
}

func TestDiff_MappingRemoved_Delete(t *testing.T) {
	d := New(nil, nil, nil)
	oldManifest := testManifestWithMappings()
	newManifest := testManifest()
	newManifest.Mappings = nil

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceMapping)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "stripe_webhook:1", deletes[0].ResourceCode)
	assert.True(t, deletes[0].Breaking)
	assert.True(t, plan.HasBreakingChanges)
}

func TestDiff_MappingUnchanged_NoChange(t *testing.T) {
	d := New(nil, nil, nil)
	manifest := testManifestWithMappings()

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	noChanges := filterActionsByResource(plan.Actions, ActionNoChange, ResourceMapping)
	assert.Len(t, noChanges, 1)
	assert.Equal(t, "stripe_webhook:1", noChanges[0].ResourceCode)
}

func TestDiff_MappingModified_Update(t *testing.T) {
	d := New(nil, nil, nil)
	oldManifest := testManifestWithMappings()

	newManifest := testManifestWithMappings()
	newManifest.Mappings[0].TargetRpc = "UpdatePaymentOrder"

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceMapping)
	assert.Len(t, updates, 1)
	assert.Equal(t, "stripe_webhook:1", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "target_rpc")
}

func TestDiff_MappingModifiedTargetService_DescribesChange(t *testing.T) {
	d := New(nil, nil, nil)
	oldManifest := testManifestWithMappings()

	newManifest := testManifestWithMappings()
	newManifest.Mappings[0].TargetService = "meridian.other.v1.OtherService"

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceMapping)
	assert.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "target_service")
}

func TestDiff_MappingModifiedStatus_DescribesChange(t *testing.T) {
	d := New(nil, nil, nil)
	oldManifest := testManifestWithMappings()

	newManifest := testManifestWithMappings()
	newManifest.Mappings[0].Status = mappingv1.MappingStatus_MAPPING_STATUS_DEPRECATED

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceMapping)
	assert.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "status")
}

func TestDiff_MappingKey_NameVersionComposite(t *testing.T) {
	// Two mappings with same name but different versions are distinct resources
	d := New(nil, nil, nil)
	oldManifest := testManifest()
	oldManifest.Mappings = []*mappingv1.MappingDefinition{
		testMapping("stripe_webhook", 1),
	}

	newManifest := testManifest()
	newManifest.Mappings = []*mappingv1.MappingDefinition{
		testMapping("stripe_webhook", 1),
		testMapping("stripe_webhook", 2),
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceMapping)
	assert.Len(t, creates, 1)
	assert.Equal(t, "stripe_webhook:2", creates[0].ResourceCode)

	noChanges := filterActionsByResource(plan.Actions, ActionNoChange, ResourceMapping)
	assert.Len(t, noChanges, 1)
	assert.Equal(t, "stripe_webhook:1", noChanges[0].ResourceCode)
}

func TestDiff_NilLastApplied_MappingCreated(t *testing.T) {
	d := New(nil, nil, nil)
	manifest := testManifestWithMappings()

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceMapping)
	assert.Len(t, creates, 1)
	assert.Equal(t, "stripe_webhook:1", creates[0].ResourceCode)
}

func TestMappingKey(t *testing.T) {
	assert.Equal(t, "stripe_webhook:1", mappingKey("stripe_webhook", 1))
	assert.Equal(t, "my_mapping:42", mappingKey("my_mapping", 42))
}
