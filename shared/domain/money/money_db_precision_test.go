package money_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestMoney_DatabasePrecision_RoundTrip validates that decimal.Decimal values
// maintain exact precision when stored in and retrieved from CockroachDB/PostgreSQL
// DECIMAL columns. This tests the database layer's ability to preserve monetary
// precision without rounding or truncation.
//
// Test strategy:
// 1. Create a test table with DECIMAL(28,9) column (28 total digits, 9 decimal places)
// 2. Insert test values using shopspring/decimal
// 3. Retrieve values back from database
// 4. Verify byte-level equality between original and retrieved values
func TestMoney_DatabasePrecision_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create test table with DECIMAL column
	// DECIMAL(28,9) matches shopspring/decimal's precision capabilities
	// 28 total digits, 9 decimal places
	_, err := pool.Exec(ctx, `
		CREATE TABLE test_money_precision (
			id SERIAL PRIMARY KEY,
			amount DECIMAL(28, 9) NOT NULL,
			currency VARCHAR(3) NOT NULL,
			description TEXT
		)
	`)
	require.NoError(t, err, "failed to create test table")

	tests := []struct {
		name        string
		amountStr   string
		currency    money.Currency
		description string
	}{
		{
			name:        "standard 2 decimal places - GBP",
			amountStr:   "100.00",
			currency:    money.CurrencyGBP,
			description: "typical monetary amount",
		},
		{
			name:        "standard 2 decimal places - USD",
			amountStr:   "123.45",
			currency:    money.CurrencyUSD,
			description: "standard currency amount",
		},
		{
			name:        "zero decimal places - JPY",
			amountStr:   "1000",
			currency:    money.CurrencyJPY,
			description: "zero-decimal currency",
		},
		{
			name:        "3 decimal places - extended precision",
			amountStr:   "100.123",
			currency:    money.CurrencyEUR,
			description: "three decimal place precision",
		},
		{
			name:        "4 decimal places - forex rates",
			amountStr:   "1.2345",
			currency:    money.CurrencyUSD,
			description: "forex exchange rate precision",
		},
		{
			name:        "5 decimal places - crypto conversion",
			amountStr:   "0.12345",
			currency:    money.CurrencyUSD,
			description: "high-precision conversion",
		},
		{
			name:        "6 decimal places - scientific calculations",
			amountStr:   "999.123456",
			currency:    money.CurrencyGBP,
			description: "scientific precision",
		},
		{
			name:        "7 decimal places - algorithmic trading",
			amountStr:   "1234.1234567",
			currency:    money.CurrencyEUR,
			description: "algorithmic trading precision",
		},
		{
			name:        "8 decimal places - high frequency",
			amountStr:   "5678.12345678",
			currency:    money.CurrencyUSD,
			description: "high-frequency trading precision",
		},
		{
			name:        "9 decimal places - maximum supported precision",
			amountStr:   "100.123456789",
			currency:    money.CurrencyGBP,
			description: "maximum DECIMAL(28,9) precision",
		},
		{
			name:        "negative amount - 2 decimals",
			amountStr:   "-50.25",
			currency:    money.CurrencyGBP,
			description: "negative monetary value",
		},
		{
			name:        "negative amount - 9 decimals",
			amountStr:   "-999.987654321",
			currency:    money.CurrencyUSD,
			description: "negative high-precision value",
		},
		{
			name:        "zero amount",
			amountStr:   "0.00",
			currency:    money.CurrencyUSD,
			description: "zero value",
		},
		{
			name:        "minimum positive value",
			amountStr:   "0.000000001",
			currency:    money.CurrencyEUR,
			description: "smallest representable value",
		},
		{
			name:        "very large amount - billions",
			amountStr:   "9999999999.99",
			currency:    money.CurrencyUSD,
			description: "large institutional amount",
		},
		{
			name:        "very large amount with high precision",
			amountStr:   "123456789.123456789",
			currency:    money.CurrencyGBP,
			description: "large amount with maximum precision",
		},
		{
			name:        "recurring decimal representation",
			amountStr:   "10.333333333",
			currency:    money.CurrencyUSD,
			description: "repeating decimal pattern",
		},
		{
			name:        "banker's rounding edge case - half even",
			amountStr:   "100.5",
			currency:    money.CurrencyGBP,
			description: "half value for rounding tests",
		},
		{
			name:        "maximum safe integer boundary",
			amountStr:   "9007199254740992.00", // 2^53
			currency:    money.CurrencyUSD,
			description: "JavaScript safe integer boundary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse decimal amount
			originalAmount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err, "failed to parse test decimal: %s", tt.amountStr)

			// Create Money instance
			originalMoney, err := money.New(originalAmount, tt.currency)
			require.NoError(t, err, "failed to create Money")

			// Insert into database
			var insertedID int
			err = pool.QueryRow(ctx,
				`INSERT INTO test_money_precision (amount, currency, description)
				 VALUES ($1, $2, $3)
				 RETURNING id`,
				originalMoney.Amount().String(), // Store as string to preserve precision
				originalMoney.CurrencyCode(),
				tt.description,
			).Scan(&insertedID)
			require.NoError(t, err, "failed to insert money into database")

			// Retrieve from database
			var retrievedAmountStr string
			var retrievedCurrency string
			err = pool.QueryRow(ctx,
				`SELECT amount, currency FROM test_money_precision WHERE id = $1`,
				insertedID,
			).Scan(&retrievedAmountStr, &retrievedCurrency)
			require.NoError(t, err, "failed to retrieve money from database")

			// Parse retrieved decimal
			retrievedAmount, err := decimal.NewFromString(retrievedAmountStr)
			require.NoError(t, err, "failed to parse retrieved decimal")

			// Reconstruct Money from database values
			retrievedMoneyCurrency, err := money.ParseCurrency(retrievedCurrency)
			require.NoError(t, err, "failed to parse retrieved currency")

			retrievedMoney, err := money.New(retrievedAmount, retrievedMoneyCurrency)
			require.NoError(t, err, "failed to create Money from database values")

			// Verify exact equality - no precision loss
			assert.True(t, originalMoney.Equals(retrievedMoney),
				"database round-trip precision loss detected:\noriginal:  %s\nretrieved: %s",
				originalMoney.Amount().String(),
				retrievedMoney.Amount().String())

			// Verify currency preserved
			assert.Equal(t, originalMoney.Currency(), retrievedMoney.Currency(),
				"currency changed during database round-trip")

			// Verify string representation is identical
			assert.Equal(t, originalAmount.String(), retrievedAmount.String(),
				"decimal string representation differs after database round-trip")

			// Clean up this test's data
			_, err = pool.Exec(ctx, `DELETE FROM test_money_precision WHERE id = $1`, insertedID)
			require.NoError(t, err, "failed to clean up test data")
		})
	}
}

// TestMoney_DatabasePrecision_BulkOperations validates precision preservation
// during bulk insert and retrieval operations, ensuring that batch processing
// doesn't introduce precision errors.
func TestMoney_DatabasePrecision_BulkOperations(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create test table
	_, err := pool.Exec(ctx, `
		CREATE TABLE test_bulk_precision (
			id SERIAL PRIMARY KEY,
			amount DECIMAL(28, 9) NOT NULL,
			currency VARCHAR(3) NOT NULL
		)
	`)
	require.NoError(t, err, "failed to create test table")

	// Generate 100 test values with varying precision
	testCases := make([]struct {
		amount   decimal.Decimal
		currency money.Currency
	}, 100)

	for i := 0; i < 100; i++ {
		// Create amounts with different precision levels (0-9 decimal places)
		decimalPlaces := i % 10
		amountStr := "123.123456789"
		if decimalPlaces < 9 {
			amountStr = amountStr[:4+decimalPlaces] // "123." + N decimal places
		}

		amount, err := decimal.NewFromString(amountStr)
		require.NoError(t, err)

		// Rotate through currencies
		currencies := []money.Currency{
			money.CurrencyGBP,
			money.CurrencyUSD,
			money.CurrencyEUR,
			money.CurrencyJPY,
		}
		currency := currencies[i%len(currencies)]

		testCases[i] = struct {
			amount   decimal.Decimal
			currency money.Currency
		}{amount, currency}
	}

	// Bulk insert using transaction
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	for _, tc := range testCases {
		_, err := tx.Exec(ctx,
			`INSERT INTO test_bulk_precision (amount, currency) VALUES ($1, $2)`,
			tc.amount.String(),
			string(tc.currency),
		)
		require.NoError(t, err, "bulk insert failed")
	}

	err = tx.Commit(ctx)
	require.NoError(t, err, "failed to commit bulk insert")

	// Bulk retrieve and verify
	rows, err := pool.Query(ctx,
		`SELECT amount, currency FROM test_bulk_precision ORDER BY id`,
	)
	require.NoError(t, err, "failed to query bulk data")
	defer rows.Close()

	retrievedCount := 0
	for rows.Next() {
		var amountStr string
		var currencyStr string
		err := rows.Scan(&amountStr, &currencyStr)
		require.NoError(t, err, "failed to scan row")

		retrievedAmount, err := decimal.NewFromString(amountStr)
		require.NoError(t, err, "failed to parse retrieved amount")

		// Verify against original
		original := testCases[retrievedCount]
		assert.True(t, original.amount.Equal(retrievedAmount),
			"bulk operation precision mismatch at index %d: expected %s, got %s",
			retrievedCount, original.amount.String(), retrievedAmount.String())

		retrievedCount++
	}

	require.NoError(t, rows.Err())
	assert.Equal(t, len(testCases), retrievedCount, "row count mismatch")
}

// TestMoney_DatabasePrecision_AggregateOperations validates that database
// aggregate functions (SUM, AVG) preserve precision correctly.
func TestMoney_DatabasePrecision_AggregateOperations(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create test table
	_, err := pool.Exec(ctx, `
		CREATE TABLE test_aggregate_precision (
			id SERIAL PRIMARY KEY,
			amount DECIMAL(28, 9) NOT NULL,
			currency VARCHAR(3) NOT NULL
		)
	`)
	require.NoError(t, err, "failed to create test table")

	// Insert test amounts
	testAmounts := []string{
		"100.123456789",
		"200.987654321",
		"300.555555555",
		"50.111111111",
	}

	expectedSum := decimal.Zero
	for _, amountStr := range testAmounts {
		amount, err := decimal.NewFromString(amountStr)
		require.NoError(t, err)

		_, err = pool.Exec(ctx,
			`INSERT INTO test_aggregate_precision (amount, currency) VALUES ($1, $2)`,
			amountStr,
			string(money.CurrencyGBP),
		)
		require.NoError(t, err)

		expectedSum = expectedSum.Add(amount)
	}

	// Test SUM aggregate
	var sumStr string
	err = pool.QueryRow(ctx,
		`SELECT SUM(amount)::TEXT FROM test_aggregate_precision`,
	).Scan(&sumStr)
	require.NoError(t, err)

	retrievedSum, err := decimal.NewFromString(sumStr)
	require.NoError(t, err)

	assert.True(t, expectedSum.Equal(retrievedSum),
		"SUM aggregate precision mismatch: expected %s, got %s",
		expectedSum.String(), retrievedSum.String())

	// Test AVG aggregate
	expectedAvg := expectedSum.Div(decimal.NewFromInt(int64(len(testAmounts))))

	var avgStr string
	err = pool.QueryRow(ctx,
		`SELECT AVG(amount)::TEXT FROM test_aggregate_precision`,
	).Scan(&avgStr)
	require.NoError(t, err)

	retrievedAvg, err := decimal.NewFromString(avgStr)
	require.NoError(t, err)

	// For AVG, allow small rounding differences (within 1 nano unit)
	diff := expectedAvg.Sub(retrievedAvg).Abs()
	maxDiff := decimal.NewFromFloat(0.000000001)
	assert.True(t, diff.LessThanOrEqual(maxDiff),
		"AVG aggregate precision loss exceeds tolerance: expected %s, got %s (diff: %s)",
		expectedAvg.String(), retrievedAvg.String(), diff.String())
}

// TestMoney_DatabasePrecision_EdgeCases validates database handling of
// edge cases like maximum precision, very large values, and boundary conditions.
func TestMoney_DatabasePrecision_EdgeCases(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create test table with different precision configurations
	_, err := pool.Exec(ctx, `
		CREATE TABLE test_edge_cases (
			id SERIAL PRIMARY KEY,
			amount_28_9 DECIMAL(28, 9),   -- Maximum shopspring/decimal precision
			amount_19_4 DECIMAL(19, 4),   -- Standard financial precision
			amount_10_2 DECIMAL(10, 2),   -- Typical currency precision
			description TEXT
		)
	`)
	require.NoError(t, err, "failed to create test table")

	tests := []struct {
		name           string
		amount28_9Str  string
		amount19_4Str  string
		amount10_2Str  string
		description    string
		expectTruncate map[string]bool // which columns expect truncation
	}{
		{
			name:          "maximum DECIMAL(28,9) value",
			amount28_9Str: "9999999999999999999.999999999", // 19 int digits + 9 decimal
			amount19_4Str: "999999999999999.9999",          // Fits in 19,4
			amount10_2Str: "99999999.99",                   // Fits in 10,2
			description:   "maximum representable values for each precision",
		},
		{
			name:          "minimum positive values",
			amount28_9Str: "0.000000001",
			amount19_4Str: "0.0001",
			amount10_2Str: "0.01",
			description:   "smallest positive values",
		},
		{
			name:          "trailing zeros preservation",
			amount28_9Str: "100.100000000",
			amount19_4Str: "100.1000",
			amount10_2Str: "100.10",
			description:   "verify trailing zeros are handled correctly",
		},
		{
			name:          "leading zeros",
			amount28_9Str: "000000001.000000001",
			amount19_4Str: "000000001.0001",
			amount10_2Str: "000000001.01",
			description:   "leading zeros should not affect value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Insert test values
			_, err := pool.Exec(ctx,
				`INSERT INTO test_edge_cases (amount_28_9, amount_19_4, amount_10_2, description)
				 VALUES ($1, $2, $3, $4)`,
				tt.amount28_9Str,
				tt.amount19_4Str,
				tt.amount10_2Str,
				tt.description,
			)
			require.NoError(t, err, "failed to insert edge case")

			// Retrieve and verify each column
			var retrieved28_9, retrieved19_4, retrieved10_2 string
			err = pool.QueryRow(ctx,
				`SELECT amount_28_9::TEXT, amount_19_4::TEXT, amount_10_2::TEXT
				 FROM test_edge_cases
				 WHERE description = $1`,
				tt.description,
			).Scan(&retrieved28_9, &retrieved19_4, &retrieved10_2)
			require.NoError(t, err, "failed to retrieve edge case")

			// Verify DECIMAL(28,9) precision
			original28_9, err := decimal.NewFromString(tt.amount28_9Str)
			require.NoError(t, err)
			retrieved28_9Dec, err := decimal.NewFromString(retrieved28_9)
			require.NoError(t, err)
			assert.True(t, original28_9.Equal(retrieved28_9Dec),
				"DECIMAL(28,9) precision mismatch: %s != %s",
				original28_9.String(), retrieved28_9Dec.String())

			// Verify DECIMAL(19,4) precision
			original19_4, err := decimal.NewFromString(tt.amount19_4Str)
			require.NoError(t, err)
			retrieved19_4Dec, err := decimal.NewFromString(retrieved19_4)
			require.NoError(t, err)
			assert.True(t, original19_4.Equal(retrieved19_4Dec),
				"DECIMAL(19,4) precision mismatch: %s != %s",
				original19_4.String(), retrieved19_4Dec.String())

			// Verify DECIMAL(10,2) precision
			original10_2, err := decimal.NewFromString(tt.amount10_2Str)
			require.NoError(t, err)
			retrieved10_2Dec, err := decimal.NewFromString(retrieved10_2)
			require.NoError(t, err)
			assert.True(t, original10_2.Equal(retrieved10_2Dec),
				"DECIMAL(10,2) precision mismatch: %s != %s",
				original10_2.String(), retrieved10_2Dec.String())
		})
	}
}

// setupTestDatabase creates a PostgreSQL testcontainer and returns a connection pool.
// The cleanup function should be deferred to ensure proper resource cleanup.
func setupTestDatabase(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Create PostgreSQL container (CockroachDB-compatible)
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_money_precision"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "failed to start PostgreSQL container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	require.NoError(t, err, "failed to create connection pool")

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		if pool != nil {
			pool.Close()
		}
		if pgContainer != nil {
			_ = pgContainer.Terminate(cleanupCtx)
		}
	}

	return pool, cleanup
}
