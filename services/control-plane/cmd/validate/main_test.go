package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// --- printResult ---

func TestPrintResult_Valid(t *testing.T) {
	output := captureStdout(t, func() {
		printResult("test.json", &validator.ValidationResult{Valid: true})
	})
	assert.Contains(t, output, "PASS test.json")
}

func TestPrintResult_Invalid_WithErrors(t *testing.T) {
	output := captureStdout(t, func() {
		printResult("test.json", &validator.ValidationResult{
			Valid: false,
			Errors: []validator.ValidationError{
				{
					Code:       "MISSING_FIELD",
					Path:       "instruments[0].code",
					Message:    "required field missing",
					Suggestion: "add the code field",
				},
			},
		})
	})
	assert.Contains(t, output, "FAIL test.json")
	assert.Contains(t, output, "ERROR [MISSING_FIELD]")
	assert.Contains(t, output, "instruments[0].code")
	assert.Contains(t, output, "required field missing")
	assert.Contains(t, output, "suggestion: add the code field")
}

func TestPrintResult_WithWarnings(t *testing.T) {
	output := captureStdout(t, func() {
		printResult("test.json", &validator.ValidationResult{
			Valid: true,
			Warnings: []validator.ValidationError{
				{Code: "DEPRECATED", Path: "sagas[0]", Message: "deprecated handler"},
			},
		})
	})
	assert.Contains(t, output, "PASS test.json")
	assert.Contains(t, output, "WARN  [DEPRECATED]")
}

func TestPrintResult_NoSuggestion(t *testing.T) {
	output := captureStdout(t, func() {
		printResult("test.json", &validator.ValidationResult{
			Valid: false,
			Errors: []validator.ValidationError{
				{Code: "ERR", Path: "x", Message: "bad"},
			},
		})
	})
	assert.NotContains(t, output, "suggestion:")
}

// --- validateFile ---

func TestValidateFile_ValidManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := minimalManifest()

	data, err := protojson.Marshal(manifest)
	require.NoError(t, err)
	path := filepath.Join(dir, "test.json")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	v, err := validator.New()
	require.NoError(t, err)

	result, err := validateFile(v, path)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestValidateFile_NonexistentFile(t *testing.T) {
	v, err := validator.New()
	require.NoError(t, err)

	_, err = validateFile(v, "/nonexistent/file.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read file")
}

func TestValidateFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))

	v, err := validator.New()
	require.NoError(t, err)

	_, err = validateFile(v, path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse manifest JSON")
}

// --- validateManifests ---

func TestValidateManifests_InvalidGlob(t *testing.T) {
	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	failed := validateManifests("[invalid", s, false)
	assert.True(t, failed)
}

func TestValidateManifests_NoMatches(t *testing.T) {
	dir := t.TempDir()
	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	failed := validateManifests(filepath.Join(dir, "*.json"), s, false)
	assert.True(t, failed)
}

func TestValidateManifests_ValidFile(t *testing.T) {
	dir := t.TempDir()
	manifest := minimalManifest()
	data, err := protojson.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0o644))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	// Use JSON output mode to capture the result and verify the file was processed
	output := captureStdout(t, func() {
		_ = validateManifests(filepath.Join(dir, "*.json"), derivedSchema, true)
	})
	assert.Contains(t, output, "test.json")
}

func TestValidateManifests_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	manifest := minimalManifest()
	data, err := protojson.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0o644))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	output := captureStdout(t, func() {
		_ = validateManifests(filepath.Join(dir, "*.json"), derivedSchema, true)
	})
	// JSON output should contain the file path
	assert.Contains(t, output, "test.json")
}

func TestValidateManifests_InvalidFileContent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0o644))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	failed := validateManifests(filepath.Join(dir, "*.json"), derivedSchema, false)
	assert.True(t, failed)
}

func TestValidateManifests_InvalidFileContent_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0o644))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	output := captureStdout(t, func() {
		failed := validateManifests(filepath.Join(dir, "*.json"), derivedSchema, true)
		assert.True(t, failed)
	})
	assert.Contains(t, output, "error")
}

// --- validateStarlark (the wrapper in main.go) ---

func TestValidateStarlarkWrapper_ValidFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.star"), []byte("x = 1"), 0o644))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	failed := validateStarlark(filepath.Join(dir, "*.star"), derivedSchema, false)
	assert.False(t, failed)
}

func TestValidateStarlarkWrapper_NoMatches(t *testing.T) {
	dir := t.TempDir()
	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	failed := validateStarlark(filepath.Join(dir, "*.star"), derivedSchema, false)
	assert.True(t, failed)
}

func TestValidateStarlarkWrapper_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.star"), []byte("x = 1"), 0o644))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	output := captureStdout(t, func() {
		failed := validateStarlark(filepath.Join(dir, "*.star"), derivedSchema, true)
		assert.False(t, failed)
	})
	assert.Contains(t, output, "test.star")
	assert.Contains(t, output, "true") // "pass": true
}

func TestValidateStarlarkWrapper_FailedFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.star"), []byte("def foo(\n"), 0o644))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	failed := validateStarlark(filepath.Join(dir, "*.star"), derivedSchema, false)
	assert.True(t, failed)
}

func TestValidateStarlarkWrapper_SkippedFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "skip.star"),
		[]byte("# schema-validation: skip\nx = 1"),
		0o644,
	))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	output := captureStdout(t, func() {
		failed := validateStarlark(filepath.Join(dir, "*.star"), derivedSchema, false)
		assert.False(t, failed)
	})
	assert.Contains(t, output, "SKIP")
}

func TestValidateStarlarkWrapper_JSONOutput_SkippedFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "skip.star"),
		[]byte("# schema-validation: skip\nx = 1"),
		0o644,
	))

	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)

	output := captureStdout(t, func() {
		_ = validateStarlark(filepath.Join(dir, "*.star"), derivedSchema, true)
	})
	assert.Contains(t, output, `"skipped"`)
}

// --- Helpers ---

func minimalManifest() *controlplanev1.Manifest {
	seedData, _ := structpb.NewStruct(map[string]interface{}{})
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:        "Test Manifest",
			Industry:    "testing",
			Description: "A test manifest for unit tests",
		},
		SeedData: seedData,
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)

	return buf.String()
}

// captureStderr captures stderr output during execution of fn.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	return buf.String()
}

// Ensure captureStderr is referenced for potential use in test variants.
var _ = captureStderr
