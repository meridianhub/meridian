package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// grpcCodeNames maps numeric gRPC status codes to their canonical string names.
// These correspond to google.golang.org/grpc/codes.Code values.
var grpcCodeNames = map[int]string{
	0:  "OK",
	1:  "CANCELLED",
	2:  "UNKNOWN",
	3:  "INVALID_ARGUMENT",
	4:  "DEADLINE_EXCEEDED",
	5:  "NOT_FOUND",
	6:  "ALREADY_EXISTS",
	7:  "PERMISSION_DENIED",
	8:  "RESOURCE_EXHAUSTED",
	9:  "FAILED_PRECONDITION",
	10: "ABORTED",
	11: "OUT_OF_RANGE",
	12: "UNIMPLEMENTED",
	13: "INTERNAL",
	14: "UNAVAILABLE",
	15: "DATA_LOSS",
	16: "UNAUTHENTICATED",
}

// vanguardErrorBody is the JSON structure that Vanguard emits for error responses.
// It follows the google.rpc.Status format with a numeric code.
// Pointer fields allow presence detection: a missing field stays nil, enabling
// us to distinguish a real Vanguard body from generic JSON errors.
type vanguardErrorBody struct {
	Code    *int              `json:"code"`
	Message *string           `json:"message"`
	Details []json.RawMessage `json:"details"`
}

// canonicalErrorBody is our API's error response format, which uses a human-readable
// string code alongside the error message.
type canonicalErrorBody struct {
	Error   string            `json:"error"`
	Code    string            `json:"code"`
	Details []json.RawMessage `json:"details,omitempty"`
}

// errorReformattingWriter intercepts the response to capture the status and body,
// allowing the middleware to rewrite error responses before they are sent to the client.
type errorReformattingWriter struct {
	http.ResponseWriter
	statusCode  int
	body        bytes.Buffer
	wroteHeader bool
}

func (w *errorReformattingWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = statusCode
	w.wroteHeader = true
}

func (w *errorReformattingWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

// Flush implements http.Flusher so that underlying response writers that
// implement http.Flusher are not broken by the wrapper.
func (w *errorReformattingWriter) Flush() {
	// Buffer is flushed in the middleware after rewriting; nothing to do here.
}

// errorReformattingMiddleware wraps an HTTP handler and rewrites error responses
// (non-2xx with application/json body) from Vanguard's google.rpc.Status format
// into the canonical API error format:
//
//	{"error": "<message>", "code": "<GRPC_CODE_NAME>", "details": [...]}
//
// This provides a consistent, human-readable error body for REST API consumers.
func errorReformattingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &errorReformattingWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		// Pass through successful responses without modification.
		if rw.statusCode >= http.StatusOK && rw.statusCode < http.StatusMultipleChoices {
			w.WriteHeader(rw.statusCode)
			_, _ = w.Write(rw.body.Bytes())
			return
		}

		// Attempt to reformat the error body if it looks like JSON.
		ct := w.Header().Get("Content-Type")
		bodyBytes := rw.body.Bytes()

		var vErr vanguardErrorBody
		if isJSONContentType(ct) && json.Unmarshal(bodyBytes, &vErr) == nil && vErr.Code != nil && vErr.Message != nil {
			codeName := grpcCodeName(*vErr.Code)
			canonical := canonicalErrorBody{
				Error:   *vErr.Message,
				Code:    codeName,
				Details: vErr.Details,
			}
			out, err := json.Marshal(canonical)
			if err == nil {
				// Remove Content-Length so it is not stale after reformatting.
				// The marshaled body length differs from the original.
				w.Header().Del("Content-Length")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(rw.statusCode)
				_, _ = w.Write(out)
				return
			}
		}

		// Fall through: pass the original response unmodified.
		w.WriteHeader(rw.statusCode)
		_, _ = w.Write(bodyBytes)
	})
}

// grpcCodeName returns the canonical string name for a gRPC status code integer.
// Unknown codes fall back to "UNKNOWN".
func grpcCodeName(code int) string {
	if name, ok := grpcCodeNames[code]; ok {
		return name
	}
	return "UNKNOWN"
}

// isJSONContentType reports whether the content-type header indicates JSON.
// Media type tokens are compared case-insensitively per RFC 2616 §3.7.
func isJSONContentType(ct string) bool {
	// Strip parameters (everything after the first ';').
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}
