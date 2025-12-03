package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReport(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Second)
	report := NewReport()
	after := time.Now().UTC().Add(time.Second).Truncate(time.Second)

	// Verify demo ID format
	assert.True(t, strings.HasPrefix(report.DemoID, "horizon-proof-"),
		"DemoID should start with 'horizon-proof-'")

	// Verify timestamp is RFC3339 and within expected range
	// Note: RFC3339 has second precision, so we compare truncated times
	ts, err := time.Parse(time.RFC3339, report.Timestamp)
	require.NoError(t, err, "Timestamp should be valid RFC3339")
	assert.True(t, !ts.Before(before) && !ts.After(after),
		"Timestamp should be within test execution window (before=%v, ts=%v, after=%v)",
		before, ts, after)

	// Verify attempts slice is initialized
	assert.NotNil(t, report.Attempts)
	assert.Empty(t, report.Attempts)
}

func TestReport_CalculateVerdict_Passed(t *testing.T) {
	report := &Report{
		Verification: VerificationReport{
			RequestsSent:         2,
			TransactionsRecorded: 1,
			BalanceCorrect:       true,
			NoDoubleSpend:        true,
		},
	}

	verdict := report.CalculateVerdict()
	assert.Equal(t, ReportVerdictPassed, verdict)
}

func TestReport_CalculateVerdict_Failed(t *testing.T) {
	tests := []struct {
		name         string
		verification VerificationReport
	}{
		{
			name: "balance incorrect",
			verification: VerificationReport{
				RequestsSent:         2,
				TransactionsRecorded: 1,
				BalanceCorrect:       false,
				NoDoubleSpend:        true,
			},
		},
		{
			name: "double spend detected",
			verification: VerificationReport{
				RequestsSent:         2,
				TransactionsRecorded: 2, // Double spend!
				BalanceCorrect:       false,
				NoDoubleSpend:        false,
			},
		},
		{
			name: "no transaction recorded",
			verification: VerificationReport{
				RequestsSent:         2,
				TransactionsRecorded: 0, // No transaction executed
				BalanceCorrect:       false,
				NoDoubleSpend:        false,
			},
		},
		{
			name: "all checks fail",
			verification: VerificationReport{
				RequestsSent:         2,
				TransactionsRecorded: 2,
				BalanceCorrect:       false,
				NoDoubleSpend:        false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := &Report{Verification: tt.verification}
			verdict := report.CalculateVerdict()
			assert.Equal(t, ReportVerdictFailed, verdict)
		})
	}
}

func TestReport_AddAttempt(t *testing.T) {
	report := NewReport()

	// Add first attempt (timeout)
	attempt1 := AttemptReport{
		Attempt:        1,
		IdempotencyKey: "HORIZON-TXN-123",
		Status:         StatusClientTimeout,
		Error:          "context deadline exceeded",
		DurationMs:     45,
	}
	report.AddAttempt(attempt1)

	assert.Len(t, report.Attempts, 1)
	assert.Equal(t, 1, report.Verification.RequestsSent)
	assert.Equal(t, attempt1, report.Attempts[0])

	// Add second attempt (success)
	attempt2 := AttemptReport{
		Attempt:        2,
		IdempotencyKey: "HORIZON-TXN-123",
		Status:         StatusSuccess,
		PaymentOrderID: "po_abc123",
		DurationMs:     120,
	}
	report.AddAttempt(attempt2)

	assert.Len(t, report.Attempts, 2)
	assert.Equal(t, 2, report.Verification.RequestsSent)
	assert.Equal(t, attempt2, report.Attempts[1])
}

func TestReport_SetAccountInfo(t *testing.T) {
	report := NewReport()
	report.SetAccountInfo("acc_test123", 100000, 90000)

	assert.Equal(t, "acc_test123", report.Account.ID)
	assert.Equal(t, int64(100000), report.Account.InitialBalanceCents)
	assert.Equal(t, int64(90000), report.Account.ExpectedBalanceCents)
}

func TestReport_SetFinalBalance(t *testing.T) {
	t.Run("balance correct with single transaction", func(t *testing.T) {
		report := NewReport()
		report.SetAccountInfo("acc_test", 100000, 90000)
		report.SetFinalBalance(90000, 1)

		assert.Equal(t, int64(90000), report.Account.FinalBalanceCents)
		assert.True(t, report.Verification.BalanceCorrect)
		assert.True(t, report.Verification.NoDoubleSpend)
		assert.Equal(t, 1, report.Verification.TransactionsRecorded)
		assert.Equal(t, ReportVerdictPassed, report.Verdict)
	})

	t.Run("double spend detected", func(t *testing.T) {
		report := NewReport()
		report.SetAccountInfo("acc_test", 100000, 90000)
		report.SetFinalBalance(80000, 2) // Balance shows double deduction

		assert.Equal(t, int64(80000), report.Account.FinalBalanceCents)
		assert.False(t, report.Verification.BalanceCorrect)
		assert.False(t, report.Verification.NoDoubleSpend)
		assert.Equal(t, 2, report.Verification.TransactionsRecorded)
		assert.Equal(t, ReportVerdictFailed, report.Verdict)
	})

	t.Run("no transaction executed", func(t *testing.T) {
		report := NewReport()
		report.SetAccountInfo("acc_test", 100000, 90000)
		report.SetFinalBalance(100000, 0) // Balance unchanged

		assert.Equal(t, int64(100000), report.Account.FinalBalanceCents)
		assert.False(t, report.Verification.BalanceCorrect)
		assert.False(t, report.Verification.NoDoubleSpend)
		assert.Equal(t, 0, report.Verification.TransactionsRecorded)
		assert.Equal(t, ReportVerdictFailed, report.Verdict)
	})
}

func TestReport_ToJSON(t *testing.T) {
	report := &Report{
		DemoID:    "horizon-proof-1701607800",
		Timestamp: "2024-12-03T10:30:00Z",
		Account: AccountReport{
			ID:                   "acc_xxx",
			InitialBalanceCents:  100000,
			FinalBalanceCents:    90000,
			ExpectedBalanceCents: 90000,
		},
		Attempts: []AttemptReport{
			{
				Attempt:        1,
				IdempotencyKey: "HORIZON-TXN-xxx",
				Status:         StatusClientTimeout,
				Error:          "context deadline exceeded",
				DurationMs:     45,
			},
			{
				Attempt:        2,
				IdempotencyKey: "HORIZON-TXN-xxx",
				Status:         StatusSuccess,
				PaymentOrderID: "po_xxx",
				DurationMs:     120,
			},
		},
		Verification: VerificationReport{
			RequestsSent:         2,
			TransactionsRecorded: 1,
			BalanceCorrect:       true,
			NoDoubleSpend:        true,
		},
		Verdict: ReportVerdictPassed,
	}

	data, err := report.ToJSON()
	require.NoError(t, err)

	// Verify it's valid JSON
	var parsed Report
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	// Verify content matches
	assert.Equal(t, report.DemoID, parsed.DemoID)
	assert.Equal(t, report.Timestamp, parsed.Timestamp)
	assert.Equal(t, report.Account, parsed.Account)
	assert.Equal(t, report.Attempts, parsed.Attempts)
	assert.Equal(t, report.Verification, parsed.Verification)
	assert.Equal(t, report.Verdict, parsed.Verdict)

	// Verify it's indented (pretty-printed)
	assert.Contains(t, string(data), "\n  ")
}

func TestReport_ToJSON_OmitsEmptyFields(t *testing.T) {
	report := &Report{
		DemoID:    "test",
		Timestamp: "2024-12-03T10:30:00Z",
		Attempts: []AttemptReport{
			{
				Attempt:        1,
				IdempotencyKey: "key",
				Status:         StatusSuccess,
				DurationMs:     100,
				// Error and PaymentOrderID are empty
			},
		},
		Verdict: ReportVerdictPassed,
	}

	data, err := report.ToJSON()
	require.NoError(t, err)

	// Verify empty fields are omitted
	jsonStr := string(data)
	assert.NotContains(t, jsonStr, `"error"`)
	assert.NotContains(t, jsonStr, `"payment_order_id"`)
}

func TestReport_WriteToFile(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "horizon-demo-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	report := &Report{
		DemoID:    "horizon-proof-test",
		Timestamp: "2024-12-03T10:30:00Z",
		Account: AccountReport{
			ID:                   "acc_test",
			InitialBalanceCents:  100000,
			FinalBalanceCents:    90000,
			ExpectedBalanceCents: 90000,
		},
		Attempts: []AttemptReport{
			{
				Attempt:        1,
				IdempotencyKey: "HORIZON-TXN-test",
				Status:         StatusSuccess,
				PaymentOrderID: "po_test",
				DurationMs:     100,
			},
		},
		Verification: VerificationReport{
			RequestsSent:         1,
			TransactionsRecorded: 1,
			BalanceCorrect:       true,
			NoDoubleSpend:        true,
		},
		Verdict: ReportVerdictPassed,
	}

	outputPath := filepath.Join(tmpDir, "integrity_report.json")
	err = report.WriteToFile(outputPath)
	require.NoError(t, err)

	// Verify file exists and has correct content
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	var loaded Report
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	assert.Equal(t, report.DemoID, loaded.DemoID)
	assert.Equal(t, report.Verdict, loaded.Verdict)

	// Verify file permissions (should be 0600)
	info, err := os.Stat(outputPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestReport_WriteToFile_InvalidPath(t *testing.T) {
	report := NewReport()

	// Try to write to a non-existent directory
	err := report.WriteToFile("/nonexistent/path/report.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing report")
}

func TestReport_FullScenario_Passed(t *testing.T) {
	// Simulate a complete demo run that passes
	report := NewReport()

	// Setup account
	report.SetAccountInfo("acc_horizon_test", 100000, 90000)

	// Attempt 1: Timeout
	report.AddAttempt(AttemptReport{
		Attempt:        1,
		IdempotencyKey: "HORIZON-TXN-12345",
		Status:         StatusClientTimeout,
		Error:          "context deadline exceeded",
		DurationMs:     45,
	})

	// Attempt 2: Success
	report.AddAttempt(AttemptReport{
		Attempt:        2,
		IdempotencyKey: "HORIZON-TXN-12345",
		Status:         StatusSuccess,
		PaymentOrderID: "po_abc123",
		DurationMs:     120,
	})

	// Verification
	report.SetFinalBalance(90000, 1)

	// Assert final state
	assert.Equal(t, ReportVerdictPassed, report.Verdict)
	assert.Equal(t, 2, report.Verification.RequestsSent)
	assert.Equal(t, 1, report.Verification.TransactionsRecorded)
	assert.True(t, report.Verification.BalanceCorrect)
	assert.True(t, report.Verification.NoDoubleSpend)
}

func TestReport_FullScenario_DoubleSpend(t *testing.T) {
	// Simulate a demo run that detects double-spend
	report := NewReport()

	report.SetAccountInfo("acc_horizon_test", 100000, 90000)

	report.AddAttempt(AttemptReport{
		Attempt:        1,
		IdempotencyKey: "HORIZON-TXN-12345",
		Status:         StatusSuccess, // First request succeeded
		PaymentOrderID: "po_abc123",
		DurationMs:     100,
	})

	report.AddAttempt(AttemptReport{
		Attempt:        2,
		IdempotencyKey: "HORIZON-TXN-12345",
		Status:         StatusSuccess, // Second request also succeeded (BAD!)
		PaymentOrderID: "po_def456",   // Different order ID = double spend
		DurationMs:     100,
	})

	// Final balance shows double deduction
	report.SetFinalBalance(80000, 2)

	// Assert failure
	assert.Equal(t, ReportVerdictFailed, report.Verdict)
	assert.False(t, report.Verification.BalanceCorrect)
	assert.False(t, report.Verification.NoDoubleSpend)
}

func TestReport_FullScenario_NoTransaction(t *testing.T) {
	// Simulate a demo where no transaction was recorded
	report := NewReport()

	report.SetAccountInfo("acc_horizon_test", 100000, 90000)

	report.AddAttempt(AttemptReport{
		Attempt:        1,
		IdempotencyKey: "HORIZON-TXN-12345",
		Status:         StatusClientTimeout,
		Error:          "context deadline exceeded",
		DurationMs:     45,
	})

	report.AddAttempt(AttemptReport{
		Attempt:        2,
		IdempotencyKey: "HORIZON-TXN-12345",
		Status:         AttemptStatusError,
		Error:          "service unavailable",
		DurationMs:     200,
	})

	// Balance unchanged, no transactions
	report.SetFinalBalance(100000, 0)

	// Assert failure
	assert.Equal(t, ReportVerdictFailed, report.Verdict)
	assert.False(t, report.Verification.BalanceCorrect)
	assert.False(t, report.Verification.NoDoubleSpend)
	assert.Equal(t, 0, report.Verification.TransactionsRecorded)
}

func TestReport_JSONSchema_MatchesPRD(t *testing.T) {
	// This test verifies the JSON output matches the PRD specification exactly
	report := &Report{
		DemoID:    "horizon-proof-1701607800",
		Timestamp: "2024-12-03T10:30:00Z",
		Account: AccountReport{
			ID:                   "acc_xxx",
			InitialBalanceCents:  100000,
			FinalBalanceCents:    90000,
			ExpectedBalanceCents: 90000,
		},
		Attempts: []AttemptReport{
			{
				Attempt:        1,
				IdempotencyKey: "HORIZON-TXN-xxx",
				Status:         "CLIENT_TIMEOUT",
				Error:          "context deadline exceeded",
				DurationMs:     45,
			},
			{
				Attempt:        2,
				IdempotencyKey: "HORIZON-TXN-xxx",
				Status:         "SUCCESS",
				PaymentOrderID: "po_xxx",
				DurationMs:     120,
			},
		},
		Verification: VerificationReport{
			RequestsSent:         2,
			TransactionsRecorded: 1,
			BalanceCorrect:       true,
			NoDoubleSpend:        true,
		},
		Verdict: "PASSED",
	}

	data, err := report.ToJSON()
	require.NoError(t, err)

	// Parse back to verify structure
	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	// Verify top-level fields exist with correct types
	assert.IsType(t, "", parsed["demo_id"])
	assert.IsType(t, "", parsed["timestamp"])
	assert.IsType(t, map[string]any{}, parsed["account"])
	assert.IsType(t, []any{}, parsed["attempts"])
	assert.IsType(t, map[string]any{}, parsed["verification"])
	assert.IsType(t, "", parsed["verdict"])

	// Verify account fields
	account := parsed["account"].(map[string]any)
	assert.Contains(t, account, "id")
	assert.Contains(t, account, "initial_balance_cents")
	assert.Contains(t, account, "final_balance_cents")
	assert.Contains(t, account, "expected_balance_cents")

	// Verify verification fields
	verification := parsed["verification"].(map[string]any)
	assert.Contains(t, verification, "requests_sent")
	assert.Contains(t, verification, "transactions_recorded")
	assert.Contains(t, verification, "balance_correct")
	assert.Contains(t, verification, "no_double_spend")
}
