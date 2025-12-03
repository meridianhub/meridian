package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, "localhost:50051", cfg.Target)
	assert.Equal(t, 30*time.Millisecond, cfg.Timeout)
	assert.Equal(t, int64(10000), cfg.Amount)
	assert.Equal(t, "./integrity_report.json", cfg.Output)
	assert.False(t, cfg.Verbose)
	assert.False(t, cfg.NoCleanup)
}

func TestValidateConfig_Valid(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "default config",
			cfg:  DefaultConfig(),
		},
		{
			name: "minimum timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 5 * time.Millisecond,
				Amount:  100,
				Output:  "./report.json",
			},
		},
		{
			name: "maximum timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 10 * time.Second,
				Amount:  100,
				Output:  "./report.json",
			},
		},
		{
			name: "large amount",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  100000000, // GBP 1,000,000.00
				Output:  "./report.json",
			},
		},
		{
			name: "custom target",
			cfg: &Config{
				Target:  "payment-order.default.svc.cluster.local:50054",
				Timeout: 30 * time.Millisecond,
				Amount:  10000,
				Output:  "/tmp/integrity_report.json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			assert.NoError(t, err)
		})
	}
}

func TestValidateConfig_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		wantError error
	}{
		{
			name: "empty target",
			cfg: &Config{
				Target:  "",
				Timeout: 30 * time.Millisecond,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTargetEmpty,
		},
		{
			name: "zero timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 0,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutNotPositive,
		},
		{
			name: "negative timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: -1 * time.Second,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutNotPositive,
		},
		{
			name: "timeout too short",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 1 * time.Millisecond,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutTooShort,
		},
		{
			name: "timeout too long",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 11 * time.Second,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutTooLong,
		},
		{
			name: "zero amount",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  0,
				Output:  "./report.json",
			},
			wantError: ErrAmountNotPositive,
		},
		{
			name: "negative amount",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  -100,
				Output:  "./report.json",
			},
			wantError: ErrAmountNotPositive,
		},
		{
			name: "amount exceeds maximum",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  100000001, // Over GBP 1,000,000.00
				Output:  "./report.json",
			},
			wantError: ErrAmountTooLarge,
		},
		{
			name: "empty output",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  10000,
				Output:  "",
			},
			wantError: ErrOutputEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			require.Error(t, err)
			assert.True(t, errors.Is(err, tt.wantError), "expected error %v, got %v", tt.wantError, err)
		})
	}
}

func TestValidateConfig_BoundaryConditions(t *testing.T) {
	// Test exact boundary values
	t.Run("minimum valid timeout boundary", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 5 * time.Millisecond, // Exact minimum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})

	t.Run("just below minimum timeout", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 4 * time.Millisecond, // Just below minimum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.Error(t, validateConfig(cfg))
	})

	t.Run("maximum valid timeout boundary", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 10 * time.Second, // Exact maximum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})

	t.Run("just above maximum timeout", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 10*time.Second + time.Millisecond, // Just above maximum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.Error(t, validateConfig(cfg))
	})

	t.Run("minimum valid amount", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 30 * time.Millisecond,
			Amount:  1, // Minimum valid (1 pence)
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})

	t.Run("maximum valid amount", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 30 * time.Millisecond,
			Amount:  100000000, // Maximum valid (GBP 1,000,000.00)
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})
}

func TestExitCodes(t *testing.T) {
	assert.Equal(t, 0, ExitCodePassed)
	assert.Equal(t, 1, ExitCodeFailed)
	assert.Equal(t, 2, ExitCodeError)
}

func TestStepStatus_String(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected string
	}{
		{StatusOK, "OK"},
		{StatusTimeout, "TIMEOUT"},
		{StatusFailed, "FAIL"},
		{StatusPassed, "PASSED"},
		{StatusError, "ERROR"},
		{StepStatus(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestStepStatus_Color(t *testing.T) {
	tests := []struct {
		status        StepStatus
		expectedColor string
	}{
		{StatusOK, colorGreen},
		{StatusPassed, colorGreen},
		{StatusFailed, colorRed},
		{StatusError, colorRed},
		{StatusTimeout, colorYellow},
		{StepStatus(99), colorReset},
	}

	for _, tt := range tests {
		t.Run(tt.status.String(), func(t *testing.T) {
			assert.Equal(t, tt.expectedColor, tt.status.color())
		})
	}
}

func TestVerdict_String(t *testing.T) {
	tests := []struct {
		verdict  Verdict
		expected string
	}{
		{VerdictPassed, "PASSED"},
		{VerdictFailed, "FAILED"},
		{VerdictError, "ERROR"},
		{Verdict(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.verdict.String())
		})
	}
}

func TestVerdict_ExitCode(t *testing.T) {
	tests := []struct {
		verdict      Verdict
		expectedCode int
	}{
		{VerdictPassed, ExitCodePassed},
		{VerdictFailed, ExitCodeFailed},
		{VerdictError, ExitCodeError},
		{Verdict(99), ExitCodeError}, // Unknown defaults to error
	}

	for _, tt := range tests {
		t.Run(tt.verdict.String(), func(t *testing.T) {
			assert.Equal(t, tt.expectedCode, tt.verdict.exitCode())
		})
	}
}

func TestVerdict_Color(t *testing.T) {
	tests := []struct {
		verdict       Verdict
		expectedColor string
	}{
		{VerdictPassed, colorGreen},
		{VerdictFailed, colorRed},
		{VerdictError, colorYellow},
		{Verdict(99), colorYellow}, // Unknown defaults to yellow
	}

	for _, tt := range tests {
		t.Run(tt.verdict.String(), func(t *testing.T) {
			assert.Equal(t, tt.expectedColor, tt.verdict.color())
		})
	}
}

func TestPrintASCIITable(t *testing.T) {
	result := &DemoResult{
		Steps: []StepResult{
			{Step: 1, Name: "Create Account", Status: StatusOK, Details: "acc_123"},
			{Step: 2, Name: "Deposit Funds", Status: StatusOK, Details: "GBP 1000"},
			{Step: 3, Name: "Payment Attempt 1", Status: StatusTimeout, Details: "deadline exceeded"},
			{Step: 4, Name: "Payment Attempt 2", Status: StatusOK, Details: "idempotent"},
			{Step: 5, Name: "Verify Balance", Status: StatusPassed, Details: "correct"},
			{Step: 6, Name: "Verify Orders", Status: StatusPassed, Details: "1 order"},
		},
		Verdict: VerdictPassed,
	}

	var buf bytes.Buffer
	printASCIITable(&buf, result)
	output := buf.String()

	// Verify header
	assert.Contains(t, output, "HORIZON INTEGRITY PROOF")
	assert.Contains(t, output, "STEP")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "DETAILS")

	// Verify steps are present
	assert.Contains(t, output, "Create Account")
	assert.Contains(t, output, "Deposit Funds")
	assert.Contains(t, output, "Payment Attempt 1")
	assert.Contains(t, output, "Payment Attempt 2")
	assert.Contains(t, output, "Verify Balance")
	assert.Contains(t, output, "Verify Orders")

	// Verify status values
	assert.Contains(t, output, "OK")
	assert.Contains(t, output, "TIMEOUT")
	assert.Contains(t, output, "PASSED")

	// Verify verdict
	assert.Contains(t, output, "VERDICT:")
	assert.Contains(t, output, "PASSED")
	assert.Contains(t, output, "No phantom transactions, no double-spend")
}

func TestPrintASCIITable_FailedVerdict(t *testing.T) {
	result := &DemoResult{
		Steps: []StepResult{
			{Step: 1, Name: "Create Account", Status: StatusOK, Details: "acc_123"},
			{Step: 2, Name: "Verify Balance", Status: StatusFailed, Details: "expected 900, got 800"},
		},
		Verdict: VerdictFailed,
	}

	var buf bytes.Buffer
	printASCIITable(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "VERDICT:")
	assert.Contains(t, output, "FAILED")
	assert.Contains(t, output, "Integrity issue detected")
}

func TestPrintASCIITable_ErrorVerdict(t *testing.T) {
	result := &DemoResult{
		Steps: []StepResult{
			{Step: 1, Name: "Connect", Status: StatusError, Details: "connection refused"},
		},
		Verdict: VerdictError,
	}

	var buf bytes.Buffer
	printASCIITable(&buf, result)
	output := buf.String()

	assert.Contains(t, output, "VERDICT:")
	assert.Contains(t, output, "ERROR")
	assert.Contains(t, output, "Execution error occurred")
}

func TestPrintASCIITable_ContainsANSICodes(t *testing.T) {
	result := &DemoResult{
		Steps: []StepResult{
			{Step: 1, Name: "Test Step", Status: StatusOK, Details: "done"},
		},
		Verdict: VerdictPassed,
	}

	var buf bytes.Buffer
	printASCIITable(&buf, result)
	output := buf.String()

	// Verify ANSI codes are present (green for OK/PASSED)
	assert.True(t, strings.Contains(output, colorGreen), "should contain green ANSI code")
	assert.True(t, strings.Contains(output, colorReset), "should contain reset ANSI code")
	assert.True(t, strings.Contains(output, colorBold), "should contain bold ANSI code")
	assert.True(t, strings.Contains(output, colorCyan), "should contain cyan ANSI code for header")
}

func TestGetVerdictMessage(t *testing.T) {
	tests := []struct {
		verdict  Verdict
		expected string
	}{
		{VerdictPassed, "No phantom transactions, no double-spend"},
		{VerdictFailed, "Integrity issue detected"},
		{VerdictError, "Execution error occurred"},
		{Verdict(99), "Unknown result"},
	}

	for _, tt := range tests {
		t.Run(tt.verdict.String(), func(t *testing.T) {
			assert.Equal(t, tt.expected, getVerdictMessage(tt.verdict))
		})
	}
}

func TestDemoResult_Structure(t *testing.T) {
	result := &DemoResult{
		Steps: []StepResult{
			{Step: 1, Name: "Step One", Status: StatusOK, Details: "detail1"},
			{Step: 2, Name: "Step Two", Status: StatusPassed, Details: "detail2"},
		},
		Verdict: VerdictPassed,
	}

	require.Len(t, result.Steps, 2)
	assert.Equal(t, 1, result.Steps[0].Step)
	assert.Equal(t, "Step One", result.Steps[0].Name)
	assert.Equal(t, StatusOK, result.Steps[0].Status)
	assert.Equal(t, "detail1", result.Steps[0].Details)
	assert.Equal(t, VerdictPassed, result.Verdict)
}

func TestExecuteCleanup_NoError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := executeCleanup(logger, "test-account-id")
	assert.NoError(t, err)
}

func TestPrintASCIITable_EmptySteps(t *testing.T) {
	result := &DemoResult{
		Steps:   []StepResult{},
		Verdict: VerdictPassed,
	}

	var buf bytes.Buffer
	printASCIITable(&buf, result)
	output := buf.String()

	// Should still have header and verdict even with no steps
	assert.Contains(t, output, "HORIZON INTEGRITY PROOF")
	assert.Contains(t, output, "VERDICT:")
	assert.Contains(t, output, "PASSED")
}
