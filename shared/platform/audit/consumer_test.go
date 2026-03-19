package audit

import (
	"testing"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
)

func TestIsValidSchemaName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid simple", "acme_bank", true},
		{"valid with numbers", "tenant_123", true},
		{"valid underscore prefix", "_private", true},
		{"valid uppercase", "Public", true},
		{"empty string", "", false},
		{"starts with number", "123tenant", false},
		{"contains hyphen", "acme-bank", false},
		{"contains space", "acme bank", false},
		{"contains dot", "acme.bank", false},
		{"contains semicolon", "acme;drop", false},
		{"SQL injection attempt", "acme'; DROP TABLE--", false},
		{"too long", string(make([]byte, 64)), false},
		{"max length", "a" + string(make([]byte, 62)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fill the byte slices with valid chars for the length tests
			if tt.name == "too long" {
				input := make([]byte, 64)
				for i := range input {
					input[i] = 'a'
				}
				tt.input = string(input)
			}
			if tt.name == "max length" {
				input := make([]byte, 63)
				for i := range input {
					input[i] = 'a'
				}
				tt.input = string(input)
			}

			got := isValidSchemaName(tt.input)
			if got != tt.expected {
				t.Errorf("isValidSchemaName(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple name", "acme_bank", `"acme_bank"`},
		{"with double quote", `acme"bank`, `"acme""bank"`},
		{"empty string", "", `""`},
		{"already quoted", `"test"`, `"""test"""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quoteIdentifier(tt.input)
			if got != tt.expected {
				t.Errorf("quoteIdentifier(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestProtoToOperation(t *testing.T) {
	tests := []struct {
		name     string
		op       auditv1.AuditOperation
		expected string
	}{
		{"insert", auditv1.AuditOperation_AUDIT_OPERATION_INSERT, "INSERT"},
		{"update", auditv1.AuditOperation_AUDIT_OPERATION_UPDATE, "UPDATE"},
		{"delete", auditv1.AuditOperation_AUDIT_OPERATION_DELETE, "DELETE"},
		{"initial import", auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT, "INITIAL_IMPORT"},
		{"unspecified", auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED, ""},
		{"unknown value", auditv1.AuditOperation(999), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protoToOperation(tt.op)
			if got != tt.expected {
				t.Errorf("protoToOperation(%v) = %q, want %q", tt.op, got, tt.expected)
			}
		})
	}
}

func TestNewConsumer_Validation(t *testing.T) {
	t.Run("empty bootstrap servers", func(t *testing.T) {
		_, err := NewConsumer(ConsumerConfig{})
		if err != ErrEmptyBootstrapServers {
			t.Errorf("expected ErrEmptyBootstrapServers, got %v", err)
		}
	})

	t.Run("nil database", func(t *testing.T) {
		_, err := NewConsumer(ConsumerConfig{
			BootstrapServers: "localhost:9092",
		})
		if err != ErrNilDatabase {
			t.Errorf("expected ErrNilDatabase, got %v", err)
		}
	})
}
