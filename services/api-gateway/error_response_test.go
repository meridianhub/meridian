package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGRPCCodeName verifies that numeric gRPC codes are converted to their
// canonical string names.
func TestGRPCCodeName(t *testing.T) {
	cases := []struct {
		code     int
		expected string
	}{
		{0, "OK"},
		{1, "CANCELLED"},
		{2, "UNKNOWN"},
		{3, "INVALID_ARGUMENT"},
		{4, "DEADLINE_EXCEEDED"},
		{5, "NOT_FOUND"},
		{6, "ALREADY_EXISTS"},
		{7, "PERMISSION_DENIED"},
		{8, "RESOURCE_EXHAUSTED"},
		{9, "FAILED_PRECONDITION"},
		{10, "ABORTED"},
		{11, "OUT_OF_RANGE"},
		{12, "UNIMPLEMENTED"},
		{13, "INTERNAL"},
		{14, "UNAVAILABLE"},
		{15, "DATA_LOSS"},
		{16, "UNAUTHENTICATED"},
		{99, "UNKNOWN"}, // out-of-range falls back to UNKNOWN
	}

	for _, tc := range cases {
		assert.Equal(t, tc.expected, grpcCodeName(tc.code), "code %d", tc.code)
	}
}

// TestErrorReformattingMiddleware_RewritesErrorBody verifies that the middleware
// rewrites Vanguard's numeric-code JSON error format to the canonical string-code format.
func TestErrorReformattingMiddleware_RewritesErrorBody(t *testing.T) {
	// Inner handler simulates what Vanguard emits for a NOT_FOUND error.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":5,"message":"party not found","details":[]}`))
	})

	handler := errorReformattingMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/parties/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "NOT_FOUND", body["code"], "code must be string NOT_FOUND")
	assert.Equal(t, "party not found", body["error"], "error must be the gRPC message")
	_, hasMessage := body["message"]
	assert.False(t, hasMessage, "must not contain Vanguard's 'message' field")
}

// TestErrorReformattingMiddleware_PassesThroughSuccessResponse verifies that
// successful (2xx) responses are not modified.
func TestErrorReformattingMiddleware_PassesThroughSuccessResponse(t *testing.T) {
	expectedBody := `{"partyId":"abc","legalName":"Alice"}`
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(expectedBody))
	})

	handler := errorReformattingMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/parties/abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, expectedBody, rec.Body.String())
}

// TestErrorReformattingMiddleware_PassesThroughNonJSONError verifies that
// non-JSON error responses (e.g. plain text "Not Found") are passed through unmodified.
func TestErrorReformattingMiddleware_PassesThroughNonJSONError(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Not Found"))
	})

	handler := errorReformattingMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "Not Found", rec.Body.String())
}

// TestErrorReformattingMiddleware_AllVanguardErrorCodes verifies that all numeric
// gRPC codes emitted by Vanguard are rewritten to their string equivalents.
func TestErrorReformattingMiddleware_AllVanguardErrorCodes(t *testing.T) {
	cases := []struct {
		numericCode int
		httpStatus  int
		stringCode  string
	}{
		{3, http.StatusBadRequest, "INVALID_ARGUMENT"},
		{4, http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"},
		{5, http.StatusNotFound, "NOT_FOUND"},
		{7, http.StatusForbidden, "PERMISSION_DENIED"},
		{13, http.StatusInternalServerError, "INTERNAL"},
		{14, http.StatusServiceUnavailable, "UNAVAILABLE"},
		{16, http.StatusUnauthorized, "UNAUTHENTICATED"},
	}

	for _, tc := range cases {
		t.Run(tc.stringCode, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.httpStatus)
				body, _ := json.Marshal(map[string]interface{}{
					"code":    tc.numericCode,
					"message": strings.ToLower(tc.stringCode),
					"details": []interface{}{},
				})
				_, _ = w.Write(body)
			})

			handler := errorReformattingMiddleware(inner)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tc.httpStatus, rec.Code)

			var result map[string]interface{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
			assert.Equal(t, tc.stringCode, result["code"])
		})
	}
}

// TestErrorReformattingMiddleware_PassesThroughNonVanguardJSON verifies that
// generic JSON error bodies without Vanguard's code/message fields are passed
// through unmodified rather than being overwritten with empty values.
func TestErrorReformattingMiddleware_PassesThroughNonVanguardJSON(t *testing.T) {
	originalBody := `{"error":"bad request"}`
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(originalBody))
	})

	handler := errorReformattingMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, originalBody, rec.Body.String(), "non-Vanguard JSON must pass through unmodified")
}

// TestErrorReformattingWriter_WriteHeaderGuard verifies that only the first
// WriteHeader call is honored, matching net/http semantics.
func TestErrorReformattingWriter_WriteHeaderGuard(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &errorReformattingWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)
	rw.WriteHeader(http.StatusInternalServerError) // should be ignored

	assert.Equal(t, http.StatusNotFound, rw.statusCode)
}

// TestIsJSONContentType verifies content-type detection logic, including
// RFC 2616 §3.7 case-insensitive media type matching.
func TestIsJSONContentType(t *testing.T) {
	// Exact match and with parameters
	assert.True(t, isJSONContentType("application/json"))
	assert.True(t, isJSONContentType("application/json; charset=utf-8"))
	assert.True(t, isJSONContentType("application/json;charset=utf-8"))
	// Mixed-case variants must also match per RFC 2616
	assert.True(t, isJSONContentType("Application/JSON"))
	assert.True(t, isJSONContentType("APPLICATION/JSON"))
	assert.True(t, isJSONContentType("application/JSON; charset=utf-8"))
	// Non-JSON types
	assert.False(t, isJSONContentType("text/plain"))
	assert.False(t, isJSONContentType("text/html"))
	assert.False(t, isJSONContentType(""))
}
