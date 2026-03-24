package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubFAClient is a configurable test implementation of FinancialAccountingClient.
type stubFAClient struct {
	detail *DiagnosticDetail
	err    error
}

func (s *stubFAClient) GetDiagnosticDetail(_ context.Context, _, _ string) (*DiagnosticDetail, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.detail, nil
}

func TestDiagnosticDetail_Fields(t *testing.T) {
	detail := &DiagnosticDetail{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		JournalEntryIDs: []string{"JE-001", "JE-002", "JE-003"},
		Message:         "Missing contra-entry for JE-001",
	}

	assert.Equal(t, "ACC-001", detail.AccountID)
	assert.Equal(t, "GBP", detail.InstrumentCode)
	assert.Equal(t, []string{"JE-001", "JE-002", "JE-003"}, detail.JournalEntryIDs)
	assert.Equal(t, "Missing contra-entry for JE-001", detail.Message)
}

func TestDiagnosticDetail_EmptyJournalEntries(t *testing.T) {
	detail := &DiagnosticDetail{
		AccountID:      "ACC-002",
		InstrumentCode: "EUR",
		Message:        "No journal entries found",
	}

	assert.Nil(t, detail.JournalEntryIDs)
	assert.Equal(t, "No journal entries found", detail.Message)
}

func TestFinancialAccountingClient_Success(t *testing.T) {
	expected := &DiagnosticDetail{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		JournalEntryIDs: []string{"JE-100"},
		Message:         "Imbalance detected in journal entry JE-100",
	}

	var client FinancialAccountingClient = &stubFAClient{detail: expected}

	result, err := client.GetDiagnosticDetail(context.Background(), "ACC-001", "GBP")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, expected.AccountID, result.AccountID)
	assert.Equal(t, expected.InstrumentCode, result.InstrumentCode)
	assert.Equal(t, expected.JournalEntryIDs, result.JournalEntryIDs)
	assert.Equal(t, expected.Message, result.Message)
}

func TestFinancialAccountingClient_Error(t *testing.T) {
	var client FinancialAccountingClient = &stubFAClient{
		err: errors.New("FA service unavailable"),
	}

	result, err := client.GetDiagnosticDetail(context.Background(), "ACC-001", "GBP")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "FA service unavailable")
}

func TestFinancialAccountingClient_NilDetail(t *testing.T) {
	var client FinancialAccountingClient = &stubFAClient{detail: nil}

	result, err := client.GetDiagnosticDetail(context.Background(), "ACC-001", "KWH")

	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestFinancialAccountingClient_MultipleInstruments(t *testing.T) {
	instruments := []string{"GBP", "EUR", "KWH", "TONNE_CO2E"}

	for _, code := range instruments {
		t.Run(code, func(t *testing.T) {
			detail := &DiagnosticDetail{
				AccountID:      "ACC-001",
				InstrumentCode: code,
				Message:        "Imbalance for " + code,
			}
			var client FinancialAccountingClient = &stubFAClient{detail: detail}

			result, err := client.GetDiagnosticDetail(context.Background(), "ACC-001", code)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, code, result.InstrumentCode)
		})
	}
}

func TestFinancialAccountingClient_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// The stub propagates the context error when the context is canceled.
	var client FinancialAccountingClient = &stubFAClient{
		err: ctx.Err(),
	}

	result, err := client.GetDiagnosticDetail(ctx, "ACC-001", "GBP")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, context.Canceled)
}
