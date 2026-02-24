package mcperrors_test

import (
	"fmt"
	"testing"

	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestFormatGRPCError_CELCompilationError verifies that a gRPC error carrying
// a CEL compilation message is parsed and line/column are extracted.
func TestFormatGRPCError_CELCompilationError(t *testing.T) {
	// CEL errors have the format: "ERROR: :1:15: undeclared reference to 'atributes'\n | ...\n | ..."
	grpcErr := status.Errorf(codes.InvalidArgument,
		"cel compilation failed: ERROR: :1:15: undeclared reference to 'atributes'")

	result := mcperrors.FormatGRPCError(grpcErr)

	if result.Valid {
		t.Fatal("expected Valid=false for error response")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error detail")
	}

	detail := result.Errors[0]
	if detail.Type != mcperrors.TypeCELCompilation {
		t.Errorf("expected type %q, got %q", mcperrors.TypeCELCompilation, detail.Type)
	}
	if detail.Line != 1 {
		t.Errorf("expected Line=1, got %d", detail.Line)
	}
	if detail.Column != 15 {
		t.Errorf("expected Column=15, got %d", detail.Column)
	}
	if detail.Message == "" {
		t.Error("expected non-empty message")
	}
}

// TestFormatGRPCError_StarlarkSyntaxError verifies Starlark syntax errors are parsed.
func TestFormatGRPCError_StarlarkSyntaxError(t *testing.T) {
	grpcErr := status.Errorf(codes.InvalidArgument,
		"starlark compilation failed: transfer.star:5:10: got end of file, want expression")

	result := mcperrors.FormatGRPCError(grpcErr)

	if result.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}

	detail := result.Errors[0]
	if detail.Type != mcperrors.TypeStarlarkSyntax {
		t.Errorf("expected type %q, got %q", mcperrors.TypeStarlarkSyntax, detail.Type)
	}
	if detail.Line != 5 {
		t.Errorf("expected Line=5, got %d", detail.Line)
	}
	if detail.Column != 10 {
		t.Errorf("expected Column=10, got %d", detail.Column)
	}
}

// TestFormatGRPCError_ManifestValidation verifies protovalidate/manifest errors are parsed.
func TestFormatGRPCError_ManifestValidation(t *testing.T) {
	grpcErr := status.Errorf(codes.InvalidArgument,
		`manifest validation failed: [{"severity":"error","path":"instruments[0].code","code":"PROTO_VALIDATION","message":"value is required"}]`)

	result := mcperrors.FormatGRPCError(grpcErr)

	if result.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}

	detail := result.Errors[0]
	if detail.Type != mcperrors.TypeManifestValidation {
		t.Errorf("expected type %q, got %q", mcperrors.TypeManifestValidation, detail.Type)
	}
	if detail.Path != "instruments[0].code" {
		t.Errorf("expected path %q, got %q", "instruments[0].code", detail.Path)
	}
}

// TestFormatGRPCError_UnknownError verifies that an unrecognized error returns a generic structure.
func TestFormatGRPCError_UnknownError(t *testing.T) {
	grpcErr := status.Errorf(codes.Internal, "something went wrong internally")

	result := mcperrors.FormatGRPCError(grpcErr)

	if result.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}

	detail := result.Errors[0]
	if detail.Type != mcperrors.TypeGeneric {
		t.Errorf("expected type %q, got %q", mcperrors.TypeGeneric, detail.Type)
	}
	if detail.Message == "" {
		t.Error("expected non-empty message")
	}
}

// TestFormatGRPCError_NilError verifies that a nil error returns a valid result.
func TestFormatGRPCError_NilError(t *testing.T) {
	result := mcperrors.FormatGRPCError(nil)

	if !result.Valid {
		t.Fatal("expected Valid=true for nil error")
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %d", len(result.Errors))
	}
}

// TestFormatGRPCError_TypoSuggestions verifies common typo suggestions are produced.
func TestFormatGRPCError_TypoSuggestions(t *testing.T) {
	tests := []struct {
		typo       string
		suggestion string
	}{
		{"atributes", "attributes"},   //nolint:misspell // intentional typo for testing
		{"ammount", "amount"},         //nolint:misspell // intentional typo for testing
		{"instruement", "instrument"}, //nolint:misspell // intentional typo for testing
		{"bukket_id", "bucket_id"},    //nolint:misspell // intentional typo for testing
	}

	for _, tc := range tests {
		t.Run(tc.typo, func(t *testing.T) {
			msg := fmt.Sprintf("cel compilation failed: ERROR: :1:1: undeclared reference to '%s'", tc.typo)
			grpcErr := status.Error(codes.InvalidArgument, msg)

			result := mcperrors.FormatGRPCError(grpcErr)
			if len(result.Errors) == 0 {
				t.Fatal("expected at least one error")
			}
			detail := result.Errors[0]
			if detail.Suggestion == "" {
				t.Errorf("expected suggestion for typo %q", tc.typo)
			}
		})
	}
}

// TestFormatGRPCError_MultipleErrors verifies that multiple validation errors
// in a JSON array are all extracted and returned.
func TestFormatGRPCError_MultipleErrors(t *testing.T) {
	grpcErr := status.Errorf(codes.InvalidArgument,
		`manifest validation failed: [{"severity":"error","path":"instruments[0].code","code":"PROTO_VALIDATION","message":"value is required"},{"severity":"error","path":"account_types[0].code","code":"PROTO_VALIDATION","message":"value is required"}]`)

	result := mcperrors.FormatGRPCError(grpcErr)

	if result.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(result.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(result.Errors))
	}
}

// TestFormatGRPCError_StarlarkUndefinedName verifies undefined Starlark names produce suggestions.
func TestFormatGRPCError_StarlarkUndefinedName(t *testing.T) {
	grpcErr := status.Errorf(codes.InvalidArgument,
		"starlark compilation failed: transfer.star:3:1: undefined: position_keepin")

	result := mcperrors.FormatGRPCError(grpcErr)

	if result.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}

	detail := result.Errors[0]
	if detail.Suggestion == "" {
		t.Errorf("expected a suggestion for 'position_keepin'")
	}
}

// TestFormatGRPCError_MultipleCELErrors verifies that multiple CEL errors on separate
// lines are all extracted rather than just the first one.
func TestFormatGRPCError_MultipleCELErrors(t *testing.T) {
	grpcErr := status.Error(codes.InvalidArgument,
		"cel compilation failed: ERROR: :1:5: undeclared reference to 'ammount'\nERROR: :2:3: undeclared reference to 'atributes'")

	result := mcperrors.FormatGRPCError(grpcErr)

	if result.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(result.Errors) != 2 {
		t.Errorf("expected 2 CEL errors, got %d", len(result.Errors))
	}
	if result.Errors[0].Line != 1 {
		t.Errorf("expected first error Line=1, got %d", result.Errors[0].Line)
	}
	if result.Errors[1].Line != 2 {
		t.Errorf("expected second error Line=2, got %d", result.Errors[1].Line)
	}
}

// TestFormatError_PlainError verifies that a plain (non-gRPC) error is handled as TypeGeneric.
func TestFormatError_PlainError(t *testing.T) {
	err := fmt.Errorf("some plain error")

	result := mcperrors.FormatGRPCError(err)

	if result.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	detail := result.Errors[0]
	if detail.Type != mcperrors.TypeGeneric {
		t.Errorf("expected type %q for plain error, got %q", mcperrors.TypeGeneric, detail.Type)
	}
}
