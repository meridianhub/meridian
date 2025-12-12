package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"go.opentelemetry.io/otel/trace"
)

// Logger provides structured JSON logging with correlation IDs
type Logger struct {
	output io.Writer
	level  LogLevel
}

// LogLevel represents the severity of a log message
type LogLevel string

// Log levels
const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp     string                 `json:"timestamp"`
	Level         LogLevel               `json:"level"`
	Message       string                 `json:"message"`
	TenantID      string                 `json:"tenant_id,omitempty"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
	TraceID       string                 `json:"trace_id,omitempty"`
	SpanID        string                 `json:"span_id,omitempty"`
	Fields        map[string]interface{} `json:"fields,omitempty"`
}

// NewLogger creates a new JSON logger
func NewLogger(output io.Writer, level LogLevel) *Logger {
	if output == nil {
		output = os.Stdout
	}
	return &Logger{
		output: output,
		level:  level,
	}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, fields ...map[string]interface{}) {
	l.log(context.Background(), LogLevelDebug, msg, fields...)
}

// DebugContext logs a debug message with context
func (l *Logger) DebugContext(ctx context.Context, msg string, fields ...map[string]interface{}) {
	l.log(ctx, LogLevelDebug, msg, fields...)
}

// Info logs an info message
func (l *Logger) Info(msg string, fields ...map[string]interface{}) {
	l.log(context.Background(), LogLevelInfo, msg, fields...)
}

// InfoContext logs an info message with context
func (l *Logger) InfoContext(ctx context.Context, msg string, fields ...map[string]interface{}) {
	l.log(ctx, LogLevelInfo, msg, fields...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, fields ...map[string]interface{}) {
	l.log(context.Background(), LogLevelWarn, msg, fields...)
}

// WarnContext logs a warning message with context
func (l *Logger) WarnContext(ctx context.Context, msg string, fields ...map[string]interface{}) {
	l.log(ctx, LogLevelWarn, msg, fields...)
}

// Error logs an error message
func (l *Logger) Error(msg string, fields ...map[string]interface{}) {
	l.log(context.Background(), LogLevelError, msg, fields...)
}

// ErrorContext logs an error message with context
func (l *Logger) ErrorContext(ctx context.Context, msg string, fields ...map[string]interface{}) {
	l.log(ctx, LogLevelError, msg, fields...)
}

// log writes a structured log entry
func (l *Logger) log(ctx context.Context, level LogLevel, msg string, fields ...map[string]interface{}) {
	// Skip if log level is below configured level
	if !l.shouldLog(level) {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		Message:   msg,
	}

	// Extract context values
	if ctx != nil {
		// Extract tenant ID from context
		if orgID, ok := tenant.FromContext(ctx); ok && !orgID.IsEmpty() {
			entry.TenantID = orgID.String()
		}

		// Extract correlation ID from context
		if correlationID := GetCorrelationID(ctx); correlationID != "" {
			entry.CorrelationID = correlationID
		}

		// Extract trace and span IDs from OpenTelemetry context
		span := trace.SpanFromContext(ctx)
		if span.SpanContext().IsValid() {
			entry.TraceID = span.SpanContext().TraceID().String()
			entry.SpanID = span.SpanContext().SpanID().String()
		}
	}

	// Merge all fields
	if len(fields) > 0 {
		entry.Fields = make(map[string]interface{})
		for _, f := range fields {
			for k, v := range f {
				entry.Fields[k] = v
			}
		}
	}

	// Write JSON to output
	data, err := json.Marshal(entry)
	if err != nil {
		// Fallback to simple error logging
		_, _ = fmt.Fprintf(l.output, "{\"level\":\"error\",\"message\":\"failed to marshal log entry\",\"error\":\"%s\"}\n", err)
		return
	}

	_, _ = fmt.Fprintf(l.output, "%s\n", data)
}

// shouldLog determines if a message at the given level should be logged
func (l *Logger) shouldLog(level LogLevel) bool {
	levels := map[LogLevel]int{
		LogLevelDebug: 0,
		LogLevelInfo:  1,
		LogLevelWarn:  2,
		LogLevelError: 3,
	}

	configuredLevel, ok := levels[l.level]
	if !ok {
		configuredLevel = levels[LogLevelInfo]
	}

	messageLevel, ok := levels[level]
	if !ok {
		return true
	}

	return messageLevel >= configuredLevel
}

// Correlation ID context key
type correlationIDKey struct{}

// WithCorrelationID adds a correlation ID to the context
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationIDKey{}, correlationID)
}

// GetCorrelationID extracts the correlation ID from the context
func GetCorrelationID(ctx context.Context) string {
	if correlationID, ok := ctx.Value(correlationIDKey{}).(string); ok {
		return correlationID
	}
	return ""
}

// PIIRedactionPatterns contains regular expressions for common PII patterns
var PIIRedactionPatterns = []*regexp.Regexp{
	// Email addresses
	regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
	// Credit card numbers (basic pattern)
	regexp.MustCompile(`\b\d{4}[\s-]?\d{4}[\s-]?\d{4}[\s-]?\d{4}\b`),
	// UK National Insurance numbers
	regexp.MustCompile(`\b[A-Z]{2}\s?\d{2}\s?\d{2}\s?\d{2}\s?[A-Z]\b`),
	// Phone numbers (UK format) - +44 and 07 variants
	regexp.MustCompile(`\+44\s?7\d{3}\s?\d{3}\s?\d{3}`),
	regexp.MustCompile(`\(?07\d{3}\)?\s?\d{3}\s?\d{3}`),
}

// RedactPII redacts personally identifiable information from a string
func RedactPII(input string) string {
	result := input
	for _, pattern := range PIIRedactionPatterns {
		result = pattern.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}

// RedactPIIFromMap redacts PII from all string values in a map
func RedactPIIFromMap(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range data {
		switch val := v.(type) {
		case string:
			result[k] = RedactPII(val)
		case map[string]interface{}:
			result[k] = RedactPIIFromMap(val)
		default:
			result[k] = v
		}
	}
	return result
}
