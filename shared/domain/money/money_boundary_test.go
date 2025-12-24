package money_test

import (
	"context"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestBoundary_ProtobufRoundTrip validates critical protobuf serialization scenarios
// that must always pass in CI. This is a consolidated subset of money_proto_test.go
// focusing on the most critical precision boundaries.
func TestBoundary_ProtobufRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		amountStr   string
		currency    money.Currency
		description string
	}{
		{
			name:        "standard GBP amount",
			amountStr:   "100.50",
			currency:    money.CurrencyGBP,
			description: "typical two-decimal currency",
		},
		{
			name:        "JPY zero decimals",
			amountStr:   "1000",
			currency:    money.CurrencyJPY,
			description: "zero-decimal currency",
		},
		{
			name:        "maximum precision",
			amountStr:   "123.456789012",
			currency:    money.CurrencyUSD,
			description: "extended precision beyond standard 2 decimals",
		},
		{
			name:        "negative amount",
			amountStr:   "-50.25",
			currency:    money.CurrencyGBP,
			description: "negative monetary value",
		},
		{
			name:        "zero amount",
			amountStr:   "0.00",
			currency:    money.CurrencyUSD,
			description: "zero value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse original amount
			originalAmount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err, "failed to parse test decimal")

			// Create Money instance
			originalMoney, err := money.New(originalAmount, tt.currency)
			require.NoError(t, err, "failed to create Money")

			// Simulate protobuf round-trip by converting to/from minor units
			// (This mimics the protobuf wire format conversion logic)
			minorUnits, err := originalMoney.ToMinorUnits()
			require.NoError(t, err, "failed to convert to minor units")

			reconstructedMoney, err := money.NewFromMinorUnits(minorUnits, tt.currency)
			require.NoError(t, err, "failed to reconstruct from minor units")

			// For currencies with decimal places, allow banker's rounding tolerance
			if tt.currency.DecimalPlaces() > 0 {
				// After round-trip through minor units, expect banker's rounding
				// Check that the difference is within the smallest minor unit
				diff := originalMoney.Amount().Sub(reconstructedMoney.Amount()).Abs()
				tolerance := decimal.NewFromFloat(0.01) // 1 cent tolerance for rounding
				assert.True(t, diff.LessThanOrEqual(tolerance),
					"protobuf round-trip precision loss exceeds tolerance: original=%s, reconstructed=%s",
					originalMoney.Amount().String(),
					reconstructedMoney.Amount().String())
			} else {
				// Zero-decimal currencies should have exact equality
				assert.True(t, originalMoney.Equals(reconstructedMoney),
					"protobuf round-trip altered zero-decimal currency: original=%s, reconstructed=%s",
					originalMoney.Amount().String(),
					reconstructedMoney.Amount().String())
			}
		})
	}
}

// TestBoundary_DatabaseRoundTrip validates critical database precision scenarios.
// This is a minimal subset of money_db_precision_test.go for fast CI execution.
func TestBoundary_DatabaseRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	ctx := context.Background()
	pool, cleanup := setupBoundaryTestDatabase(t)
	defer cleanup()

	// Create test table
	_, err := pool.Exec(ctx, `
		CREATE TABLE test_boundary_precision (
			id SERIAL PRIMARY KEY,
			amount DECIMAL(28, 9) NOT NULL,
			currency VARCHAR(3) NOT NULL
		)
	`)
	require.NoError(t, err, "failed to create test table")

	tests := []struct {
		name      string
		amountStr string
		currency  money.Currency
	}{
		{"standard precision", "123.45", money.CurrencyUSD},
		{"high precision", "100.123456789", money.CurrencyGBP},
		{"zero decimals", "1000", money.CurrencyJPY},
		{"negative amount", "-50.25", money.CurrencyGBP},
		{"very large amount", "9999999999.99", money.CurrencyUSD},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse and create original Money
			originalAmount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err)

			originalMoney, err := money.New(originalAmount, tt.currency)
			require.NoError(t, err)

			// Insert into database
			_, err = pool.Exec(ctx,
				`INSERT INTO test_boundary_precision (amount, currency) VALUES ($1, $2)`,
				originalMoney.Amount().String(),
				originalMoney.CurrencyCode(),
			)
			require.NoError(t, err, "failed to insert money")

			// Retrieve from database
			var retrievedAmountStr, retrievedCurrency string
			err = pool.QueryRow(ctx,
				`SELECT amount, currency FROM test_boundary_precision
				 WHERE amount = $1 AND currency = $2`,
				originalMoney.Amount().String(),
				originalMoney.CurrencyCode(),
			).Scan(&retrievedAmountStr, &retrievedCurrency)
			require.NoError(t, err, "failed to retrieve money")

			// Reconstruct Money
			retrievedAmount, err := decimal.NewFromString(retrievedAmountStr)
			require.NoError(t, err)

			retrievedMoneyCurrency, err := money.ParseCurrency(retrievedCurrency)
			require.NoError(t, err)

			retrievedMoney, err := money.New(retrievedAmount, retrievedMoneyCurrency)
			require.NoError(t, err)

			// Assert exact equality
			assert.True(t, originalMoney.Equals(retrievedMoney),
				"database round-trip precision loss: original=%s, retrieved=%s",
				originalMoney.Amount().String(),
				retrievedMoney.Amount().String())

			// Clean up
			_, err = pool.Exec(ctx,
				`DELETE FROM test_boundary_precision WHERE amount = $1 AND currency = $2`,
				originalMoney.Amount().String(),
				originalMoney.CurrencyCode(),
			)
			require.NoError(t, err)
		})
	}
}

// TestBoundary_BankersRounding validates critical banker's rounding cases
// that must always behave correctly. This is a minimal subset of
// money_arithmetic_rounding_test.go for CI verification.
func TestBoundary_BankersRounding(t *testing.T) {
	tests := []struct {
		name          string
		amountStr     string
		currency      money.Currency
		expectedMinor int64
		description   string
	}{
		{
			name:          "round .995 to even (up)",
			amountStr:     "100.995",
			currency:      money.CurrencyGBP,
			expectedMinor: 10100,
			description:   "10100 is even, 10099 is odd → round to 10100",
		},
		{
			name:          "round .985 to even (down)",
			amountStr:     "100.985",
			currency:      money.CurrencyGBP,
			expectedMinor: 10098,
			description:   "10098 is even, 10099 is odd → round to 10098",
		},
		{
			name:          "round .5 to even (down)",
			amountStr:     "1.005",
			currency:      money.CurrencyGBP,
			expectedMinor: 100,
			description:   "100 is even, 101 is odd → round to 100",
		},
		{
			name:          "round .5 to even (up)",
			amountStr:     "1.015",
			currency:      money.CurrencyGBP,
			expectedMinor: 102,
			description:   "102 is even, 101 is odd → round to 102",
		},
		{
			name:          "negative banker's rounding",
			amountStr:     "-100.995",
			currency:      money.CurrencyGBP,
			expectedMinor: -10100,
			description:   "negative rounds to even magnitude",
		},
		{
			name:          "JPY banker's rounding",
			amountStr:     "1234.5",
			currency:      money.CurrencyJPY,
			expectedMinor: 1234,
			description:   "1234 is even, 1235 is odd → round to 1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err)

			money, err := money.New(amount, tt.currency)
			require.NoError(t, err)

			got, err := money.ToMinorUnits()
			require.NoError(t, err)

			assert.Equal(t, tt.expectedMinor, got,
				"%s: expected %d, got %d", tt.description, tt.expectedMinor, got)
		})
	}
}

// TestBoundary_ArithmeticOperations validates that basic arithmetic maintains precision.
func TestBoundary_ArithmeticOperations(t *testing.T) {
	tests := []struct {
		name        string
		operation   func() (money.Money, error)
		expectedStr string
		currency    money.Currency
	}{
		{
			name: "add two amounts",
			operation: func() (money.Money, error) {
				m1, _ := money.New(decimal.NewFromFloat(100.50), money.CurrencyGBP)
				m2, _ := money.New(decimal.NewFromFloat(50.25), money.CurrencyGBP)
				return m1.Add(m2)
			},
			expectedStr: "150.75",
			currency:    money.CurrencyGBP,
		},
		{
			name: "subtract amounts",
			operation: func() (money.Money, error) {
				m1, _ := money.New(decimal.NewFromFloat(100.50), money.CurrencyGBP)
				m2, _ := money.New(decimal.NewFromFloat(50.25), money.CurrencyGBP)
				return m1.Subtract(m2)
			},
			expectedStr: "50.25",
			currency:    money.CurrencyGBP,
		},
		{
			name: "multiply by factor",
			operation: func() (money.Money, error) {
				m, _ := money.New(decimal.NewFromFloat(100.00), money.CurrencyGBP)
				return m.Multiply(decimal.NewFromFloat(1.5)), nil
			},
			expectedStr: "150",
			currency:    money.CurrencyGBP,
		},
		{
			name: "divide by divisor",
			operation: func() (money.Money, error) {
				m, _ := money.New(decimal.NewFromFloat(100.00), money.CurrencyGBP)
				return m.Divide(decimal.NewFromInt(2))
			},
			expectedStr: "50",
			currency:    money.CurrencyGBP,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.operation()
			require.NoError(t, err)

			expected, _ := decimal.NewFromString(tt.expectedStr)
			assert.True(t, result.Amount().Equal(expected),
				"arithmetic precision loss: expected %s, got %s",
				expected.String(),
				result.Amount().String())
			assert.Equal(t, tt.currency, result.Currency())
		})
	}
}

// TestBoundary_OverflowDetection validates that overflow errors are correctly detected.
func TestBoundary_OverflowDetection(t *testing.T) {
	tests := []struct {
		name         string
		amountStr    string
		currency     money.Currency
		shouldError  bool
		errorMessage string
	}{
		{
			name:        "safe positive value",
			amountStr:   "1000000000.00",
			currency:    money.CurrencyGBP,
			shouldError: false,
		},
		{
			name:        "safe negative value",
			amountStr:   "-1000000000.00",
			currency:    money.CurrencyGBP,
			shouldError: false,
		},
		{
			name:         "overflow positive",
			amountStr:    "92233720368547758.08", // Just over max int64 in pence
			currency:     money.CurrencyGBP,
			shouldError:  true,
			errorMessage: "overflow",
		},
		{
			name:         "overflow negative",
			amountStr:    "-92233720368547758.09", // Just under min int64 in pence
			currency:     money.CurrencyGBP,
			shouldError:  true,
			errorMessage: "overflow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err)

			money, err := money.New(amount, tt.currency)
			require.NoError(t, err)

			_, err = money.ToMinorUnits()
			if tt.shouldError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMessage)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestBoundary_CurrencyValidation validates that currency mismatches are detected.
func TestBoundary_CurrencyValidation(t *testing.T) {
	gbp100, _ := money.NewFromInt64(100, money.CurrencyGBP)
	usd100, _ := money.NewFromInt64(100, money.CurrencyUSD)

	tests := []struct {
		name      string
		operation func() error
		wantError bool
	}{
		{
			name: "add same currency - valid",
			operation: func() error {
				gbp50, _ := money.NewFromInt64(50, money.CurrencyGBP)
				_, err := gbp100.Add(gbp50)
				return err
			},
			wantError: false,
		},
		{
			name: "add different currency - error",
			operation: func() error {
				_, err := gbp100.Add(usd100)
				return err
			},
			wantError: true,
		},
		{
			name: "subtract different currency - error",
			operation: func() error {
				_, err := gbp100.Subtract(usd100)
				return err
			},
			wantError: true,
		},
		{
			name: "compare different currency - error",
			operation: func() error {
				_, err := gbp100.Compare(usd100)
				return err
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.operation()
			if tt.wantError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, money.ErrCurrencyMismatch)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestBoundary_ZeroAndNegativeHandling validates special value handling.
func TestBoundary_ZeroAndNegativeHandling(t *testing.T) {
	tests := []struct {
		name     string
		amount   decimal.Decimal
		currency money.Currency
		checks   func(*testing.T, money.Money)
	}{
		{
			name:     "zero amount",
			amount:   decimal.Zero,
			currency: money.CurrencyGBP,
			checks: func(t *testing.T, m money.Money) {
				assert.True(t, m.IsZero(), "expected IsZero to be true")
				assert.False(t, m.IsPositive(), "expected IsPositive to be false")
				assert.False(t, m.IsNegative(), "expected IsNegative to be false")

				minor, err := m.ToMinorUnits()
				require.NoError(t, err)
				assert.Equal(t, int64(0), minor)
			},
		},
		{
			name:     "positive amount",
			amount:   decimal.NewFromInt(100),
			currency: money.CurrencyGBP,
			checks: func(t *testing.T, m money.Money) {
				assert.False(t, m.IsZero())
				assert.True(t, m.IsPositive())
				assert.False(t, m.IsNegative())
			},
		},
		{
			name:     "negative amount",
			amount:   decimal.NewFromInt(-100),
			currency: money.CurrencyGBP,
			checks: func(t *testing.T, m money.Money) {
				assert.False(t, m.IsZero())
				assert.False(t, m.IsPositive())
				assert.True(t, m.IsNegative())

				// Verify negation
				negated := m.Negate()
				assert.True(t, negated.IsPositive())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := money.New(tt.amount, tt.currency)
			require.NoError(t, err)
			tt.checks(t, m)
		})
	}
}

// setupBoundaryTestDatabase creates a lightweight PostgreSQL container for boundary tests.
func setupBoundaryTestDatabase(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_boundary"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2)),
	)
	require.NoError(t, err, "failed to start PostgreSQL container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)

	cleanup := func() {
		if pool != nil {
			pool.Close()
		}
		if pgContainer != nil {
			_ = pgContainer.Terminate(context.Background())
		}
	}

	return pool, cleanup
}

// TestBoundary_MaxInt64Conversion validates safe conversion at int64 boundaries.
func TestBoundary_MaxInt64Conversion(t *testing.T) {
	tests := []struct {
		name       string
		minorUnits int64
		currency   money.Currency
		wantError  bool
	}{
		{
			name:       "max safe int64 for GBP",
			minorUnits: math.MaxInt64 - 100, // Slightly below max to avoid edge case
			currency:   money.CurrencyGBP,
			wantError:  false,
		},
		{
			name:       "min safe int64 for GBP",
			minorUnits: math.MinInt64 + 100, // Slightly above min to avoid edge case
			currency:   money.CurrencyGBP,
			wantError:  false,
		},
		{
			name:       "max int64 for JPY",
			minorUnits: math.MaxInt64 - 1,
			currency:   money.CurrencyJPY,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := money.NewFromMinorUnits(tt.minorUnits, tt.currency)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)

				// Verify round-trip
				retrieved, err := m.ToMinorUnits()
				require.NoError(t, err)
				assert.Equal(t, tt.minorUnits, retrieved)
			}
		})
	}
}
