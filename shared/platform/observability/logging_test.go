package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"go.opentelemetry.io/otel"
)

func TestNewLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	if logger == nil {
		t.Fatal("NewLogger returned nil")
		return // unreachable but satisfies staticcheck
	}

	if logger.output != buf {
		t.Error("Logger output not set correctly")
	}

	if logger.level != LogLevelInfo {
		t.Errorf("Expected level info, got %s", logger.level)
	}
}

func TestNewLogger_NilOutput(t *testing.T) {
	logger := NewLogger(nil, LogLevelInfo)

	if logger.output == nil {
		t.Error("Logger output should default to os.Stdout, not nil")
	}
}

func TestLogger_Info(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	logger.Info("test message")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.Level != LogLevelInfo {
		t.Errorf("Expected level info, got %s", entry.Level)
	}

	if entry.Message != "test message" {
		t.Errorf("Expected message 'test message', got %s", entry.Message)
	}

	if entry.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
}

func TestLogger_InfoWithFields(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	fields := map[string]interface{}{
		"user_id": "12345",
		"action":  "login",
	}

	logger.Info("user logged in", fields)

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.Fields == nil {
		t.Fatal("Fields should not be nil")
	}

	if entry.Fields["user_id"] != "12345" {
		t.Errorf("Expected user_id 12345, got %v", entry.Fields["user_id"])
	}

	if entry.Fields["action"] != "login" {
		t.Errorf("Expected action login, got %v", entry.Fields["action"])
	}
}

func TestLogger_InfoContext_CorrelationID(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	ctx := WithCorrelationID(context.Background(), "correlation-123")
	logger.InfoContext(ctx, "test message with correlation")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.CorrelationID != "correlation-123" {
		t.Errorf("Expected correlation ID correlation-123, got %s", entry.CorrelationID)
	}
}

func TestLogger_InfoContext_TenantID(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("acme_bank"))
	logger.InfoContext(ctx, "test message with tenant")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.TenantID != "acme_bank" {
		t.Errorf("Expected tenant ID acme_bank, got %s", entry.TenantID)
	}
}

func TestLogger_InfoContext_WithoutOrganization(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	logger.InfoContext(context.Background(), "test message without tenant")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.TenantID != "" {
		t.Errorf("Expected empty tenant ID, got %s", entry.TenantID)
	}
}

func TestLogger_InfoContext_AllContextValues(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	// Create context with both tenant and correlation ID
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenant.MustNewTenantID("motive"))
	ctx = WithCorrelationID(ctx, "request-456")

	logger.InfoContext(ctx, "test message with all context")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.TenantID != "motive" {
		t.Errorf("Expected tenant ID motive, got %s", entry.TenantID)
	}
	if entry.CorrelationID != "request-456" {
		t.Errorf("Expected correlation ID request-456, got %s", entry.CorrelationID)
	}
}

func TestLogger_InfoContext_TraceID(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	// Create a tracer and start a span
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-operation")
	defer span.End()

	logger.InfoContext(ctx, "test message with trace")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	// Note: TraceID and SpanID will only be populated if a TracerProvider is set up
	// In a real application with proper OTel configuration, these would be set
	// For this test, we just verify the log entry is created successfully
}

func TestLogger_Levels(t *testing.T) {
	tests := []struct {
		name          string
		logLevel      LogLevel
		logFunc       func(*Logger, string)
		shouldLog     bool
		expectedLevel LogLevel
	}{
		{"debug when debug", LogLevelDebug, func(l *Logger, msg string) { l.Debug(msg) }, true, LogLevelDebug},
		{"info when debug", LogLevelDebug, func(l *Logger, msg string) { l.Info(msg) }, true, LogLevelInfo},
		{"warn when debug", LogLevelDebug, func(l *Logger, msg string) { l.Warn(msg) }, true, LogLevelWarn},
		{"error when debug", LogLevelDebug, func(l *Logger, msg string) { l.Error(msg) }, true, LogLevelError},
		{"debug when info", LogLevelInfo, func(l *Logger, msg string) { l.Debug(msg) }, false, LogLevelDebug},
		{"info when info", LogLevelInfo, func(l *Logger, msg string) { l.Info(msg) }, true, LogLevelInfo},
		{"warn when info", LogLevelInfo, func(l *Logger, msg string) { l.Warn(msg) }, true, LogLevelWarn},
		{"error when info", LogLevelInfo, func(l *Logger, msg string) { l.Error(msg) }, true, LogLevelError},
		{"debug when warn", LogLevelWarn, func(l *Logger, msg string) { l.Debug(msg) }, false, LogLevelDebug},
		{"info when warn", LogLevelWarn, func(l *Logger, msg string) { l.Info(msg) }, false, LogLevelInfo},
		{"warn when warn", LogLevelWarn, func(l *Logger, msg string) { l.Warn(msg) }, true, LogLevelWarn},
		{"error when warn", LogLevelWarn, func(l *Logger, msg string) { l.Error(msg) }, true, LogLevelError},
		{"debug when error", LogLevelError, func(l *Logger, msg string) { l.Debug(msg) }, false, LogLevelDebug},
		{"info when error", LogLevelError, func(l *Logger, msg string) { l.Info(msg) }, false, LogLevelInfo},
		{"warn when error", LogLevelError, func(l *Logger, msg string) { l.Warn(msg) }, false, LogLevelWarn},
		{"error when error", LogLevelError, func(l *Logger, msg string) { l.Error(msg) }, true, LogLevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := NewLogger(buf, tt.logLevel)

			tt.logFunc(logger, "test message")

			if tt.shouldLog {
				if buf.Len() == 0 {
					t.Error("Expected log output, but buffer is empty")
				}

				var entry LogEntry
				if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
					t.Fatalf("Failed to parse log entry: %v", err)
				}

				if entry.Level != tt.expectedLevel {
					t.Errorf("Expected level %s, got %s", tt.expectedLevel, entry.Level)
				}
			} else {
				if buf.Len() > 0 {
					t.Error("Expected no log output, but buffer has content")
				}
			}
		})
	}
}

func TestWithCorrelationID(t *testing.T) {
	ctx := context.Background()
	correlationID := "test-correlation-123"

	ctx = WithCorrelationID(ctx, correlationID)

	retrievedID := GetCorrelationID(ctx)
	if retrievedID != correlationID {
		t.Errorf("Expected correlation ID %s, got %s", correlationID, retrievedID)
	}
}

func TestGetCorrelationID_Empty(t *testing.T) {
	ctx := context.Background()

	correlationID := GetCorrelationID(ctx)
	if correlationID != "" {
		t.Errorf("Expected empty correlation ID, got %s", correlationID)
	}
}

func TestRedactPII_Email(t *testing.T) {
	input := "User email is john.doe@example.com and jane@test.org"
	expected := "User email is [REDACTED] and [REDACTED]"

	result := RedactPII(input)
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestRedactPII_CreditCard(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"spaces",
			"Card number: 1234 5678 9012 3456",
			"Card number: [REDACTED]",
		},
		{
			"hyphens",
			"Card: 1234-5678-9012-3456",
			"Card: [REDACTED]",
		},
		{
			"no separator",
			"Card: 1234567890123456",
			"Card: [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactPII(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestRedactPII_NINumber(t *testing.T) {
	input := "NI Number: AB 12 34 56 C"
	expected := "NI Number: [REDACTED]"

	result := RedactPII(input)
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestRedactPII_PhoneNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"UK mobile +44",
			"Phone: +44 7123 456 789",
			"Phone: [REDACTED]",
		},
		{
			"UK mobile 07",
			"Call: 07123 456 789",
			"Call: [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactPII(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestRedactPII_Multiple(t *testing.T) {
	input := "Contact john@example.com or call 07123456789, card: 1234-5678-9012-3456"
	result := RedactPII(input)

	if strings.Contains(result, "john@example.com") {
		t.Error("Email should be redacted")
	}
	if strings.Contains(result, "07123456789") {
		t.Error("Phone number should be redacted")
	}
	if strings.Contains(result, "1234-5678-9012-3456") {
		t.Error("Credit card should be redacted")
	}

	// Should contain [REDACTED] multiple times
	redactedCount := strings.Count(result, "[REDACTED]")
	if redactedCount < 3 {
		t.Errorf("Expected at least 3 [REDACTED] occurrences, got %d", redactedCount)
	}
}

func TestRedactPIIFromMap(t *testing.T) {
	input := map[string]interface{}{
		"name":  "John Doe",
		"email": "john@example.com",
		"age":   30,
		"nested": map[string]interface{}{
			"contact": "jane@test.org",
		},
	}

	result := RedactPIIFromMap(input)

	// Check email was redacted
	if email, ok := result["email"].(string); ok {
		if strings.Contains(email, "john@example.com") {
			t.Error("Email should be redacted in map")
		}
	}

	// Check nested email was redacted
	if nested, ok := result["nested"].(map[string]interface{}); ok {
		if contact, ok := nested["contact"].(string); ok {
			if strings.Contains(contact, "jane@test.org") {
				t.Error("Nested email should be redacted")
			}
		}
	}

	// Check non-string values are preserved
	if age, ok := result["age"].(int); !ok || age != 30 {
		t.Error("Non-string values should be preserved")
	}
}

func TestRedactPII_NoMatch(t *testing.T) {
	input := "This is a normal message with no PII"
	result := RedactPII(input)

	if result != input {
		t.Errorf("Expected unchanged string, got %q", result)
	}
}

// Benchmark tests
func BenchmarkLogger_Info(b *testing.B) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("benchmark message")
		buf.Reset()
	}
}

func BenchmarkLogger_InfoWithFields(b *testing.B) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, LogLevelInfo)

	fields := map[string]interface{}{
		"user_id": "12345",
		"action":  "login",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("benchmark message", fields)
		buf.Reset()
	}
}

func BenchmarkRedactPII(b *testing.B) {
	input := "Contact john@example.com or call 07123456789"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RedactPII(input)
	}
}
