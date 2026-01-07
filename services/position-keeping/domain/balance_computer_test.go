package domain

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a test TransactionLogEntry
func newTestEntry(t *testing.T, amount decimal.Decimal, currency Currency, direction PostingDirection, timestamp time.Time) *TransactionLogEntry {
	t.Helper()
	money := MustNewMoney(amount, currency)
	entry, err := NewTransactionLogEntry(
		uuid.New(),
		"TEST-ACC-001",
		money,
		direction,
		timestamp,
		"Test entry",
		"TEST-REF",
		TransactionSourceManual,
	)
	require.NoError(t, err)
	return entry
}

func TestNewBalanceComputer(t *testing.T) {
	bc := NewBalanceComputer()
	require.NotNil(t, bc, "NewBalanceComputer should return a non-nil instance")
}

func TestBalanceComputer_ComputeOpening(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	tests := []struct {
		name     string
		amount   decimal.Decimal
		currency Currency
		wantType BalanceType
	}{
		{
			name:     "simple opening balance",
			amount:   decimal.NewFromInt(1000),
			currency: CurrencyGBP,
			wantType: BalanceTypeOpening,
		},
		{
			name:     "opening balance with zero amount",
			amount:   decimal.Zero,
			currency: CurrencyUSD,
			wantType: BalanceTypeOpening,
		},
		{
			name:     "opening balance with negative amount",
			amount:   decimal.NewFromInt(-500),
			currency: CurrencyEUR,
			wantType: BalanceTypeOpening,
		},
		{
			name:     "opening balance with decimal amount",
			amount:   decimal.NewFromFloat(1234.56),
			currency: CurrencyGBP,
			wantType: BalanceTypeOpening,
		},
		{
			name:     "opening balance with JPY",
			amount:   decimal.NewFromInt(100000),
			currency: CurrencyJPY,
			wantType: BalanceTypeOpening,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opening := MustNewMoney(tt.amount, tt.currency)
			result := bc.ComputeOpening(opening, now)

			assert.Equal(t, tt.wantType, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.amount))
			assert.Equal(t, string(tt.currency), result.Amount.Instrument.Code)
			assert.Equal(t, now, result.AsOf)
		})
	}
}

func TestBalanceComputer_ComputeCurrent(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	tests := []struct {
		name           string
		openingAmount  decimal.Decimal
		currency       Currency
		entries        func(t *testing.T) []*TransactionLogEntry
		expectedAmount decimal.Decimal
		wantErr        bool
		errType        error
	}{
		{
			name:          "opening plus DEBIT entries (adds)",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(1300), // 1000 + 100 + 200
			wantErr:        false,
		},
		{
			name:          "opening plus CREDIT entries (subtracts)",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionCredit, now),
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionCredit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(700), // 1000 - 100 - 200
			wantErr:        false,
		},
		{
			name:          "mixed DEBIT and CREDIT entries",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(500), CurrencyGBP, PostingDirectionDebit, now),
					newTestEntry(t, decimal.NewFromInt(300), CurrencyGBP, PostingDirectionCredit, now),
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(1300), // 1000 + 500 - 300 + 100
			wantErr:        false,
		},
		{
			name:          "empty entries returns opening balance",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(_ *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{}
			},
			expectedAmount: decimal.NewFromInt(1000),
			wantErr:        false,
		},
		{
			name:          "nil entries returns opening balance",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(_ *testing.T) []*TransactionLogEntry {
				return nil
			},
			expectedAmount: decimal.NewFromInt(1000),
			wantErr:        false,
		},
		{
			name:          "currency mismatch error",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyUSD, PostingDirectionDebit, now),
				}
			},
			expectedAmount: decimal.Zero,
			wantErr:        true,
			errType:        ErrInstrumentMismatch,
		},
		{
			name:          "zero opening with entries",
			openingAmount: decimal.Zero,
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(100),
			wantErr:        false,
		},
		{
			name:          "negative opening with entries",
			openingAmount: decimal.NewFromInt(-500),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(1000), CurrencyGBP, PostingDirectionDebit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(500), // -500 + 1000
			wantErr:        false,
		},
		{
			name:          "result can go negative (overdraft)",
			openingAmount: decimal.NewFromInt(100),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(500), CurrencyGBP, PostingDirectionCredit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(-400), // 100 - 500
			wantErr:        false,
		},
		{
			name:          "handles nil entry in slice",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
					nil, // nil entry should be skipped
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(1300), // 1000 + 100 + 200
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opening := MustNewMoney(tt.openingAmount, tt.currency)
			entries := tt.entries(t)

			result, err := bc.ComputeCurrent(opening, entries, now)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, BalanceTypeCurrent, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.expectedAmount),
				"expected %s, got %s", tt.expectedAmount, result.Amount.Amount)
			assert.Equal(t, now, result.AsOf)
		})
	}
}

func TestBalanceComputer_ComputeLedger(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	// Helper status filter that accepts all
	acceptAll := func(_ TransactionStatus) bool { return true }

	tests := []struct {
		name           string
		entries        func(t *testing.T) []*TransactionLogEntry
		expectedAmount decimal.Decimal
		wantErr        bool
		errType        error
	}{
		{
			name: "sum of entries with same currency",
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, now),
					newTestEntry(t, decimal.NewFromInt(50), CurrencyGBP, PostingDirectionCredit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(250), // 100 + 200 - 50
			wantErr:        false,
		},
		{
			name: "empty entries returns error",
			entries: func(_ *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{}
			},
			wantErr: true,
			errType: ErrNoInstrument,
		},
		{
			name: "nil entries returns error",
			entries: func(_ *testing.T) []*TransactionLogEntry {
				return nil
			},
			wantErr: true,
			errType: ErrNoInstrument,
		},
		{
			name: "currency mismatch error",
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
					newTestEntry(t, decimal.NewFromInt(200), CurrencyUSD, PostingDirectionDebit, now),
				}
			},
			wantErr: true,
			errType: ErrInstrumentMismatch,
		},
		{
			name: "single entry",
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(500), CurrencyGBP, PostingDirectionDebit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(500),
			wantErr:        false,
		},
		{
			name: "all credit entries result in negative",
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionCredit, now),
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionCredit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(-300), // -100 - 200
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := tt.entries(t)

			result, err := bc.ComputeLedger(entries, acceptAll, now)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, BalanceTypeLedger, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.expectedAmount),
				"expected %s, got %s", tt.expectedAmount, result.Amount.Amount)
			assert.Equal(t, now, result.AsOf)
		})
	}
}

func TestBalanceComputer_ComputeLedgerFromEntries(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	tests := []struct {
		name           string
		entries        func(t *testing.T) []*TransactionLogEntry
		expectedAmount decimal.Decimal
		wantErr        bool
		errType        error
	}{
		{
			name: "sum of entries",
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
					newTestEntry(t, decimal.NewFromInt(50), CurrencyGBP, PostingDirectionCredit, now),
				}
			},
			expectedAmount: decimal.NewFromInt(50), // 100 - 50
			wantErr:        false,
		},
		{
			name: "empty entries returns error",
			entries: func(_ *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{}
			},
			wantErr: true,
			errType: ErrNoInstrument,
		},
		{
			name: "currency mismatch",
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
					newTestEntry(t, decimal.NewFromInt(100), CurrencyEUR, PostingDirectionDebit, now),
				}
			},
			wantErr: true,
			errType: ErrInstrumentMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := tt.entries(t)

			result, err := bc.ComputeLedgerFromEntries(entries, now)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, BalanceTypeLedger, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.expectedAmount))
		})
	}
}

func TestBalanceComputer_ComputeReserve(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	tests := []struct {
		name     string
		amount   decimal.Decimal
		currency Currency
	}{
		{
			name:     "simple reserve amount",
			amount:   decimal.NewFromInt(500),
			currency: CurrencyGBP,
		},
		{
			name:     "zero reserve",
			amount:   decimal.Zero,
			currency: CurrencyUSD,
		},
		{
			name:     "large reserve",
			amount:   decimal.NewFromInt(1000000),
			currency: CurrencyEUR,
		},
		{
			name:     "decimal reserve amount",
			amount:   decimal.NewFromFloat(123.45),
			currency: CurrencyGBP,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reserve := MustNewMoney(tt.amount, tt.currency)
			result := bc.ComputeReserve(reserve, now)

			assert.Equal(t, BalanceTypeReserve, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.amount))
			assert.Equal(t, string(tt.currency), result.Amount.Instrument.Code)
			assert.Equal(t, now, result.AsOf)
		})
	}
}

func TestBalanceComputer_ComputeAvailable(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	tests := []struct {
		name           string
		currentAmount  decimal.Decimal
		reserveAmount  decimal.Decimal
		overdraftLimit decimal.Decimal
		currency       Currency
		expectedAmount decimal.Decimal
		wantErr        bool
		errType        error
	}{
		{
			name:           "current minus reserve plus overdraft",
			currentAmount:  decimal.NewFromInt(1000),
			reserveAmount:  decimal.NewFromInt(200),
			overdraftLimit: decimal.NewFromInt(500),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(1300), // 1000 - 200 + 500
			wantErr:        false,
		},
		{
			name:           "with zero overdraft",
			currentAmount:  decimal.NewFromInt(1000),
			reserveAmount:  decimal.NewFromInt(200),
			overdraftLimit: decimal.Zero,
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(800), // 1000 - 200 + 0
			wantErr:        false,
		},
		{
			name:           "with zero reserve",
			currentAmount:  decimal.NewFromInt(1000),
			reserveAmount:  decimal.Zero,
			overdraftLimit: decimal.NewFromInt(500),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(1500), // 1000 - 0 + 500
			wantErr:        false,
		},
		{
			name:           "with zero current",
			currentAmount:  decimal.Zero,
			reserveAmount:  decimal.NewFromInt(100),
			overdraftLimit: decimal.NewFromInt(500),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(400), // 0 - 100 + 500
			wantErr:        false,
		},
		{
			name:           "all zeros",
			currentAmount:  decimal.Zero,
			reserveAmount:  decimal.Zero,
			overdraftLimit: decimal.Zero,
			currency:       CurrencyGBP,
			expectedAmount: decimal.Zero,
			wantErr:        false,
		},
		{
			name:           "negative current (already overdrafted)",
			currentAmount:  decimal.NewFromInt(-200),
			reserveAmount:  decimal.NewFromInt(100),
			overdraftLimit: decimal.NewFromInt(500),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(200), // -200 - 100 + 500
			wantErr:        false,
		},
		{
			name:           "reserve larger than current",
			currentAmount:  decimal.NewFromInt(100),
			reserveAmount:  decimal.NewFromInt(300),
			overdraftLimit: decimal.NewFromInt(500),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(300), // 100 - 300 + 500
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := MustNewMoney(tt.currentAmount, tt.currency)
			reserve := MustNewMoney(tt.reserveAmount, tt.currency)
			overdraft := MustNewMoney(tt.overdraftLimit, tt.currency)

			result, err := bc.ComputeAvailable(current, reserve, overdraft, now)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, BalanceTypeAvailable, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.expectedAmount),
				"expected %s, got %s", tt.expectedAmount, result.Amount.Amount)
			assert.Equal(t, now, result.AsOf)
		})
	}
}

func TestBalanceComputer_ComputeAvailable_CurrencyMismatch(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	tests := []struct {
		name              string
		currentCurrency   Currency
		reserveCurrency   Currency
		overdraftCurrency Currency
	}{
		{
			name:              "reserve currency mismatch",
			currentCurrency:   CurrencyGBP,
			reserveCurrency:   CurrencyUSD,
			overdraftCurrency: CurrencyGBP,
		},
		{
			name:              "overdraft currency mismatch",
			currentCurrency:   CurrencyGBP,
			reserveCurrency:   CurrencyGBP,
			overdraftCurrency: CurrencyEUR,
		},
		{
			name:              "all different currencies",
			currentCurrency:   CurrencyGBP,
			reserveCurrency:   CurrencyUSD,
			overdraftCurrency: CurrencyEUR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := MustNewMoney(decimal.NewFromInt(1000), tt.currentCurrency)
			reserve := MustNewMoney(decimal.NewFromInt(200), tt.reserveCurrency)
			overdraft := MustNewMoney(decimal.NewFromInt(500), tt.overdraftCurrency)

			_, err := bc.ComputeAvailable(current, reserve, overdraft, now)

			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInstrumentMismatch)
		})
	}
}

func TestBalanceComputer_ComputeFree(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	tests := []struct {
		name           string
		currentAmount  decimal.Decimal
		reserveAmount  decimal.Decimal
		currency       Currency
		expectedAmount decimal.Decimal
		wantErr        bool
		errType        error
	}{
		{
			name:           "current minus reserve",
			currentAmount:  decimal.NewFromInt(1000),
			reserveAmount:  decimal.NewFromInt(200),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(800), // 1000 - 200
			wantErr:        false,
		},
		{
			name:           "with zero reserve",
			currentAmount:  decimal.NewFromInt(1000),
			reserveAmount:  decimal.Zero,
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(1000), // 1000 - 0
			wantErr:        false,
		},
		{
			name:           "reserve larger than current (negative result)",
			currentAmount:  decimal.NewFromInt(100),
			reserveAmount:  decimal.NewFromInt(500),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(-400), // 100 - 500
			wantErr:        false,
		},
		{
			name:           "both zero",
			currentAmount:  decimal.Zero,
			reserveAmount:  decimal.Zero,
			currency:       CurrencyGBP,
			expectedAmount: decimal.Zero,
			wantErr:        false,
		},
		{
			name:           "negative current",
			currentAmount:  decimal.NewFromInt(-100),
			reserveAmount:  decimal.NewFromInt(50),
			currency:       CurrencyGBP,
			expectedAmount: decimal.NewFromInt(-150), // -100 - 50
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := MustNewMoney(tt.currentAmount, tt.currency)
			reserve := MustNewMoney(tt.reserveAmount, tt.currency)

			result, err := bc.ComputeFree(current, reserve, now)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, BalanceTypeFree, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.expectedAmount),
				"expected %s, got %s", tt.expectedAmount, result.Amount.Amount)
			assert.Equal(t, now, result.AsOf)
		})
	}
}

func TestBalanceComputer_ComputeFree_CurrencyMismatch(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	current := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
	reserve := MustNewMoney(decimal.NewFromInt(200), CurrencyUSD)

	_, err := bc.ComputeFree(current, reserve, now)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentMismatch)
}

func TestBalanceComputer_ComputeClosing(t *testing.T) {
	bc := NewBalanceComputer()
	baseTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		openingAmount  decimal.Decimal
		currency       Currency
		entries        func(t *testing.T) []*TransactionLogEntry
		periodEnd      time.Time
		expectedAmount decimal.Decimal
		wantErr        bool
		errType        error
	}{
		{
			name:          "filters by timestamp correctly",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					// Before period end - included
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, baseTime.Add(1*time.Hour)),
					// Exactly at period end - included
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, periodEnd),
					// After period end - excluded
					newTestEntry(t, decimal.NewFromInt(500), CurrencyGBP, PostingDirectionDebit, periodEnd.Add(1*time.Hour)),
				}
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(1300), // 1000 + 100 + 200 (500 excluded)
			wantErr:        false,
		},
		{
			name:          "empty entries returns opening balance",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(_ *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{}
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(1000),
			wantErr:        false,
		},
		{
			name:          "nil entries returns opening balance",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(_ *testing.T) []*TransactionLogEntry {
				return nil
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(1000),
			wantErr:        false,
		},
		{
			name:          "all entries before period end",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, baseTime),
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, baseTime.Add(1*time.Hour)),
					newTestEntry(t, decimal.NewFromInt(50), CurrencyGBP, PostingDirectionCredit, baseTime.Add(2*time.Hour)),
				}
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(1250), // 1000 + 100 + 200 - 50
			wantErr:        false,
		},
		{
			name:          "all entries after period end",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, periodEnd.Add(1*time.Hour)),
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, periodEnd.Add(2*time.Hour)),
				}
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(1000), // No entries included
			wantErr:        false,
		},
		{
			name:          "currency mismatch error",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyUSD, PostingDirectionDebit, baseTime),
				}
			},
			periodEnd: periodEnd,
			wantErr:   true,
			errType:   ErrInstrumentMismatch,
		},
		{
			name:          "mixed debits and credits filtered by time",
			openingAmount: decimal.NewFromInt(1000),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(500), CurrencyGBP, PostingDirectionDebit, baseTime),                      // included
					newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionCredit, baseTime.Add(1*time.Hour)),    // included
					newTestEntry(t, decimal.NewFromInt(1000), CurrencyGBP, PostingDirectionDebit, periodEnd.Add(1*time.Second)), // excluded (after period end)
				}
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(1300), // 1000 + 500 - 200
			wantErr:        false,
		},
		{
			name:          "entry exactly at period end is included",
			openingAmount: decimal.NewFromInt(0),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, periodEnd),
				}
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(100),
			wantErr:        false,
		},
		{
			name:          "entry one nanosecond after period end is excluded",
			openingAmount: decimal.NewFromInt(0),
			currency:      CurrencyGBP,
			entries: func(t *testing.T) []*TransactionLogEntry {
				return []*TransactionLogEntry{
					newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, periodEnd.Add(1*time.Nanosecond)),
				}
			},
			periodEnd:      periodEnd,
			expectedAmount: decimal.NewFromInt(0),
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opening := MustNewMoney(tt.openingAmount, tt.currency)
			entries := tt.entries(t)

			result, err := bc.ComputeClosing(opening, entries, tt.periodEnd)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, BalanceTypeClosing, result.Type)
			assert.True(t, result.Amount.Amount.Equal(tt.expectedAmount),
				"expected %s, got %s", tt.expectedAmount, result.Amount.Amount)
			assert.Equal(t, tt.periodEnd, result.AsOf, "AsOf should equal periodEnd")
		})
	}
}

func TestBalanceComputer_SumEntries_EdgeCases(t *testing.T) {
	bc := NewBalanceComputer()
	now := time.Now().UTC()

	t.Run("handles large number of entries", func(t *testing.T) {
		entries := make([]*TransactionLogEntry, 1000)
		for i := 0; i < 1000; i++ {
			entries[i] = newTestEntry(t, decimal.NewFromInt(1), CurrencyGBP, PostingDirectionDebit, now)
		}

		opening := MustNewMoney(decimal.Zero, CurrencyGBP)
		result, err := bc.ComputeCurrent(opening, entries, now)

		require.NoError(t, err)
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(1000)))
	})

	t.Run("handles high precision decimals", func(t *testing.T) {
		entry1 := newTestEntry(t, decimal.NewFromFloat(0.01), CurrencyGBP, PostingDirectionDebit, now)
		entry2 := newTestEntry(t, decimal.NewFromFloat(0.02), CurrencyGBP, PostingDirectionDebit, now)
		entry3 := newTestEntry(t, decimal.NewFromFloat(0.03), CurrencyGBP, PostingDirectionCredit, now)

		opening := MustNewMoney(decimal.Zero, CurrencyGBP)
		result, err := bc.ComputeCurrent(opening, []*TransactionLogEntry{entry1, entry2, entry3}, now)

		require.NoError(t, err)
		// 0.01 + 0.02 - 0.03 = 0.00
		assert.True(t, result.Amount.Amount.IsZero())
	})
}

func TestBalanceComputer_Errors(t *testing.T) {
	t.Run("ErrEmptyEntries is defined", func(t *testing.T) {
		assert.NotNil(t, ErrEmptyEntries)
		assert.Contains(t, ErrEmptyEntries.Error(), "no entries")
	})

	t.Run("ErrNoInstrument is defined", func(t *testing.T) {
		assert.NotNil(t, ErrNoInstrument)
		assert.Contains(t, ErrNoInstrument.Error(), "instrument")
	})
}

// =============================================================================
// LogBalanceComputer Tests
// =============================================================================

// mockCurrentAccountClient is a test double for CurrentAccountClient
type mockCurrentAccountClient struct {
	blocks []AmountBlock
	err    error
}

func (m *mockCurrentAccountClient) GetActiveAmountBlocks(_ context.Context, _ string) ([]AmountBlock, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.blocks, nil
}

func createTestLog(t *testing.T, accountID string, entries []*TransactionLogEntry) *FinancialPositionLog {
	t.Helper()
	log, err := NewFinancialPositionLog(accountID, nil, nil)
	require.NoError(t, err)
	for _, entry := range entries {
		err := log.AddEntry(entry)
		require.NoError(t, err)
	}
	return log
}

func TestNewLogBalanceComputer(t *testing.T) {
	t.Run("creates with valid parameters", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("TEST-ACC-001", nil, nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		client := &mockCurrentAccountClient{}

		lbc, err := NewLogBalanceComputer(log, opening, client)

		require.NoError(t, err)
		require.NotNil(t, lbc)
	})

	t.Run("creates without client (nil client allowed)", func(t *testing.T) {
		log, _ := NewFinancialPositionLog("TEST-ACC-001", nil, nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)

		lbc, err := NewLogBalanceComputer(log, opening, nil)

		require.NoError(t, err)
		require.NotNil(t, lbc)
	})

	t.Run("returns error for nil log", func(t *testing.T) {
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)

		lbc, err := NewLogBalanceComputer(nil, opening, nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilLog)
		assert.Nil(t, lbc)
	})
}

func TestLogBalanceComputer_CurrentBalance(t *testing.T) {
	now := time.Now().UTC()

	t.Run("returns opening balance when no entries", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		result, err := lbc.CurrentBalance()

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeCurrent, result.Type)
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(1000)))
	})

	t.Run("adds DEBIT entries to opening balance", func(t *testing.T) {
		entries := []*TransactionLogEntry{
			newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
			newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, now),
		}
		log := createTestLog(t, "TEST-ACC-001", entries)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		result, err := lbc.CurrentBalance()

		require.NoError(t, err)
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(1300))) // 1000 + 100 + 200
	})

	t.Run("subtracts CREDIT entries from opening balance", func(t *testing.T) {
		entries := []*TransactionLogEntry{
			newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionCredit, now),
		}
		log := createTestLog(t, "TEST-ACC-001", entries)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		result, err := lbc.CurrentBalance()

		require.NoError(t, err)
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(900))) // 1000 - 100
	})
}

func TestLogBalanceComputer_ReserveBalance(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when client is nil", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		_, err := lbc.ReserveBalance(ctx)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilCurrentAccountClient)
	})

	t.Run("returns zero when no blocks", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		client := &mockCurrentAccountClient{blocks: []AmountBlock{}}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		result, err := lbc.ReserveBalance(ctx)

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeReserve, result.Type)
		assert.True(t, result.Amount.Amount.IsZero())
	})

	t.Run("sums all block amounts", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		client := &mockCurrentAccountClient{
			blocks: []AmountBlock{
				{BlockID: "B1", Amount: MustNewMoney(decimal.NewFromInt(100), CurrencyGBP), BlockType: AmountBlockTypePending},
				{BlockID: "B2", Amount: MustNewMoney(decimal.NewFromInt(200), CurrencyGBP), BlockType: AmountBlockTypePending},
				{BlockID: "B3", Amount: MustNewMoney(decimal.NewFromInt(50), CurrencyGBP), BlockType: AmountBlockTypeTemporary},
			},
		}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		result, err := lbc.ReserveBalance(ctx)

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeReserve, result.Type)
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(350))) // 100 + 200 + 50
	})

	t.Run("returns error for currency mismatch", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		client := &mockCurrentAccountClient{
			blocks: []AmountBlock{
				{BlockID: "B1", Amount: MustNewMoney(decimal.NewFromInt(100), CurrencyUSD), BlockType: AmountBlockTypePending},
			},
		}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		_, err := lbc.ReserveBalance(ctx)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInstrumentMismatch)
	})

	t.Run("propagates client errors", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		expectedErr := assert.AnError
		client := &mockCurrentAccountClient{err: expectedErr}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		_, err := lbc.ReserveBalance(ctx)

		require.Error(t, err)
		assert.ErrorIs(t, err, expectedErr)
	})
}

func TestLogBalanceComputer_AvailableBalance(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("computes Available = Current - Reserve + Overdraft", func(t *testing.T) {
		entries := []*TransactionLogEntry{
			newTestEntry(t, decimal.NewFromInt(500), CurrencyGBP, PostingDirectionDebit, now),
		}
		log := createTestLog(t, "TEST-ACC-001", entries)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		overdraft := MustNewMoney(decimal.NewFromInt(200), CurrencyGBP)
		client := &mockCurrentAccountClient{
			blocks: []AmountBlock{
				{BlockID: "B1", Amount: MustNewMoney(decimal.NewFromInt(300), CurrencyGBP), BlockType: AmountBlockTypePending},
			},
		}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		result, err := lbc.AvailableBalance(ctx, overdraft, true)

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeAvailable, result.Type)
		// Current = 1000 + 500 = 1500
		// Reserve = 300
		// Available = 1500 - 300 + 200 = 1400
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(1400)))
	})

	t.Run("ignores overdraft when disabled", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		overdraft := MustNewMoney(decimal.NewFromInt(500), CurrencyGBP)
		client := &mockCurrentAccountClient{
			blocks: []AmountBlock{
				{BlockID: "B1", Amount: MustNewMoney(decimal.NewFromInt(200), CurrencyGBP), BlockType: AmountBlockTypePending},
			},
		}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		result, err := lbc.AvailableBalance(ctx, overdraft, false)

		require.NoError(t, err)
		// Current = 1000, Reserve = 200, Overdraft = 0 (disabled)
		// Available = 1000 - 200 + 0 = 800
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(800)))
	})

	t.Run("returns error when client is nil", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		overdraft := MustNewMoney(decimal.NewFromInt(500), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		_, err := lbc.AvailableBalance(ctx, overdraft, true)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilCurrentAccountClient)
	})
}

func TestLogBalanceComputer_FreeBalance(t *testing.T) {
	ctx := context.Background()

	t.Run("computes Free = Current - Reserve", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		client := &mockCurrentAccountClient{
			blocks: []AmountBlock{
				{BlockID: "B1", Amount: MustNewMoney(decimal.NewFromInt(300), CurrencyGBP), BlockType: AmountBlockTypePending},
			},
		}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		result, err := lbc.FreeBalance(ctx)

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeFree, result.Type)
		// Free = 1000 - 300 = 700
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(700)))
	})

	t.Run("can go negative when reserve exceeds current", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(100), CurrencyGBP)
		client := &mockCurrentAccountClient{
			blocks: []AmountBlock{
				{BlockID: "B1", Amount: MustNewMoney(decimal.NewFromInt(300), CurrencyGBP), BlockType: AmountBlockTypePending},
			},
		}
		lbc, _ := NewLogBalanceComputer(log, opening, client)

		result, err := lbc.FreeBalance(ctx)

		require.NoError(t, err)
		// Free = 100 - 300 = -200
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(-200)))
	})
}

func TestLogBalanceComputer_OpeningBalance(t *testing.T) {
	t.Run("returns opening balance", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		result := lbc.OpeningBalance()

		assert.Equal(t, BalanceTypeOpening, result.Type)
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(1000)))
		assert.Equal(t, log.CreatedAt, result.AsOf)
	})
}

func TestLogBalanceComputer_ClosingBalance(t *testing.T) {
	baseTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC)

	t.Run("filters transactions by period end", func(t *testing.T) {
		entries := []*TransactionLogEntry{
			newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, baseTime.Add(1*time.Hour)),     // included
			newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, periodEnd),                     // included
			newTestEntry(t, decimal.NewFromInt(1000), CurrencyGBP, PostingDirectionDebit, periodEnd.Add(1*time.Second)), // excluded
		}
		log := createTestLog(t, "TEST-ACC-001", entries)
		opening := MustNewMoney(decimal.NewFromInt(500), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		result, err := lbc.ClosingBalance(periodEnd)

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeClosing, result.Type)
		// 500 + 100 + 200 = 800 (1000 excluded)
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(800)))
		assert.Equal(t, periodEnd, result.AsOf)
	})
}

func TestLogBalanceComputer_LedgerBalance(t *testing.T) {
	now := time.Now().UTC()

	t.Run("returns zero for empty log", func(t *testing.T) {
		log := createTestLog(t, "TEST-ACC-001", nil)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		result, err := lbc.LedgerBalance()

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeLedger, result.Type)
		assert.True(t, result.Amount.Amount.IsZero())
	})

	t.Run("sums all entries", func(t *testing.T) {
		entries := []*TransactionLogEntry{
			newTestEntry(t, decimal.NewFromInt(100), CurrencyGBP, PostingDirectionDebit, now),
			newTestEntry(t, decimal.NewFromInt(200), CurrencyGBP, PostingDirectionDebit, now),
			newTestEntry(t, decimal.NewFromInt(50), CurrencyGBP, PostingDirectionCredit, now),
		}
		log := createTestLog(t, "TEST-ACC-001", entries)
		opening := MustNewMoney(decimal.NewFromInt(1000), CurrencyGBP)
		lbc, _ := NewLogBalanceComputer(log, opening, nil)

		result, err := lbc.LedgerBalance()

		require.NoError(t, err)
		assert.Equal(t, BalanceTypeLedger, result.Type)
		// 100 + 200 - 50 = 250
		assert.True(t, result.Amount.Amount.Equal(decimal.NewFromInt(250)))
	})
}

func TestAmountBlockType_Constants(t *testing.T) {
	assert.Equal(t, AmountBlockType("PENDING"), AmountBlockTypePending)
	assert.Equal(t, AmountBlockType("FINAL"), AmountBlockTypeFinal)
	assert.Equal(t, AmountBlockType("TEMPORARY"), AmountBlockTypeTemporary)
}
