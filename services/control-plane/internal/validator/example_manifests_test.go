package validator

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

// exampleManifestsDir returns the absolute path to examples/manifests/ relative to the repo root.
func exampleManifestsDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller info")
	// Navigate from services/control-plane/internal/validator/ to repo root.
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "examples", "manifests")
}

// exampleHandlerSchema returns a schema covering every handler called by example manifests.
// When a new handler call is added to an example manifest, add its definition here
// so the CI gate catches parameter errors.
func exampleHandlerSchema() *schema.Schema {
	return &schema.Schema{
		Service: "meridian",
		Handlers: map[string]*schema.HandlerDef{
			"position_keeping.initiate_log": {
				Params: map[string]*schema.FieldDef{
					"position_id":     {Type: schema.TypeString, Required: true},
					"amount":          {Type: schema.TypeDecimal, Required: true},
					"direction":       {Type: schema.TypeEnum, Required: true, Values: []string{"CREDIT", "DEBIT"}},
					"instrument_code": {Type: schema.TypeString},
					"correlation_id":  {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"log_id": {Type: schema.TypeString},
					"status": {Type: schema.TypeString},
				},
				Compensate: "position_keeping.cancel_log",
			},
			"position_keeping.cancel_log": {
				Params: map[string]*schema.FieldDef{
					"log_id": {Type: schema.TypeString, Required: true},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			// Composite handler used by platform.json - accepts any kwargs.
			"financial_accounting.post_entries": {
				Params:     map[string]*schema.FieldDef{},
				Compensate: "financial_accounting.reverse_entries",
			},
			"financial_accounting.reverse_entries": {
				Params:               map[string]*schema.FieldDef{},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
		},
	}
}

func TestExampleManifests_PassHandlerValidation(t *testing.T) {
	dir := exampleManifestsDir(t)

	v, err := New(WithDerivedSchema(exampleHandlerSchema()))
	require.NoError(t, err)

	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "no example manifest JSON files found in %s", dir)

	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(file)
			require.NoError(t, err)

			var manifest controlplanev1.Manifest
			err = protojson.Unmarshal(data, &manifest)
			require.NoError(t, err, "failed to parse %s into proto", name)

			result := v.Validate(&manifest, nil, WithSkipImmutabilityChecks())

			for _, e := range result.Errors {
				t.Errorf("validation error [%s] at %s: %s", e.Code, e.Path, e.Message)
			}
			assert.True(t, result.Valid, "manifest %s has validation errors", name)
		})
	}
}
