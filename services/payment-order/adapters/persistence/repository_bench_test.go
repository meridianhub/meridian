package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"gorm.io/gorm"
)

// benchTime is a fixed time for consistent benchmark results
var benchTime = time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

// setupBenchDB creates a PostgreSQL testcontainer for benchmark tests.
// Note: Each benchmark spins up a new container for isolation. This adds overhead
// but ensures benchmarks don't interfere with each other and start with clean state.
//
// Known limitation: testdb.SetupPostgres requires *testing.T but benchmarks use *testing.B.
// We pass a minimal testing.T - if container setup fails, the detached T will call Fatalf
// which triggers runtime.Goexit(). The benchmark framework catches this appropriately.
// This is a pragmatic workaround until testdb.SetupPostgres accepts testing.TB.
func setupBenchDB(b *testing.B) (*gorm.DB, context.Context, func()) {
	b.Helper()
	// Use testing.T with Fatalf that properly fails the benchmark
	t := &testing.T{}
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&PaymentOrderEntity{}})
	if t.Failed() {
		b.Fatalf("setupBenchDB: testcontainer setup failed")
	}

	// Create tenant schema
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		b.Fatalf("setupBenchDB: failed to create tenant schema: %v", err)
	}

	// Create payment_orders table in tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.payment_orders (
		id UUID PRIMARY KEY,
		debtor_account_id VARCHAR(255) NOT NULL,
		creditor_reference VARCHAR(255) NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		status VARCHAR(20) NOT NULL,
		idempotency_key VARCHAR(255) NOT NULL UNIQUE,
		correlation_id VARCHAR(255),
		causation_id VARCHAR(255),
		lien_id VARCHAR(255),
		gateway_reference_id VARCHAR(255),
		ledger_booking_id VARCHAR(255),
		failure_reason TEXT,
		error_code VARCHAR(50),
		version INTEGER NOT NULL DEFAULT 1,
		lien_execution_status VARCHAR(20),
		lien_execution_attempts INTEGER DEFAULT 0,
		lien_execution_error TEXT,
		instrument_code VARCHAR(32),
		payment_attributes JSONB,
		bucket_id VARCHAR(255),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		reserved_at TIMESTAMP WITH TIME ZONE,
		executing_at TIMESTAMP WITH TIME ZONE,
		completed_at TIMESTAMP WITH TIME ZONE,
		failed_at TIMESTAMP WITH TIME ZONE,
		cancelled_at TIMESTAMP WITH TIME ZONE,
		reversed_at TIMESTAMP WITH TIME ZONE
	)`, pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		b.Fatalf("setupBenchDB: failed to create payment_orders table: %v", err)
	}

	// Set search_path to tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		b.Fatalf("setupBenchDB: failed to set search_path: %v", err)
	}

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

// BenchmarkRepository_Create benchmarks the Create operation.
// This measures the hot path for persisting new payment orders.
func BenchmarkRepository_Create(b *testing.B) {
	db, ctx, cleanup := setupBenchDB(b)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		amount, _ := domain.NewMoney("GBP", 10000)
		po, _ := domain.NewPaymentOrder(
			"acc-123",
			"cred-ref",
			amount,
			uuid.New().String(), // Unique idempotency key per iteration
			"corr-001",
		)

		err := repo.Create(ctx, po)
		if err != nil {
			b.Fatalf("Create failed: %v", err)
		}
	}
}

// BenchmarkRepository_FindByID benchmarks the FindByID operation.
// This is a primary key lookup - the most efficient read path.
func BenchmarkRepository_FindByID(b *testing.B) {
	db, ctx, cleanup := setupBenchDB(b)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	// Create a payment order to retrieve
	amount, err := domain.NewMoney("GBP", 10000)
	if err != nil {
		b.Fatalf("setup: NewMoney failed: %v", err)
	}
	po, err := domain.NewPaymentOrder("acc-123", "cred-ref", amount, "idem-key", "corr-001")
	if err != nil {
		b.Fatalf("setup: NewPaymentOrder failed: %v", err)
	}
	if err := repo.Create(ctx, po); err != nil {
		b.Fatalf("setup: Create failed: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := repo.FindByID(ctx, po.ID)
		if err != nil {
			b.Fatalf("FindByID failed: %v", err)
		}
	}
}

// BenchmarkRepository_FindByIdempotencyKey benchmarks the idempotency key lookup.
// This is used for idempotent request handling and must be fast.
func BenchmarkRepository_FindByIdempotencyKey(b *testing.B) {
	db, ctx, cleanup := setupBenchDB(b)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	// Create a payment order to retrieve
	amount, err := domain.NewMoney("GBP", 10000)
	if err != nil {
		b.Fatalf("setup: NewMoney failed: %v", err)
	}
	po, err := domain.NewPaymentOrder("acc-123", "cred-ref", amount, "bench-idem-key", "corr-001")
	if err != nil {
		b.Fatalf("setup: NewPaymentOrder failed: %v", err)
	}
	if err := repo.Create(ctx, po); err != nil {
		b.Fatalf("setup: Create failed: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := repo.FindByIdempotencyKey(ctx, "bench-idem-key")
		if err != nil {
			b.Fatalf("FindByIdempotencyKey failed: %v", err)
		}
	}
}

// BenchmarkRepository_FindByGatewayReferenceID benchmarks gateway reference lookup.
// This is used for webhook callback handling.
func BenchmarkRepository_FindByGatewayReferenceID(b *testing.B) {
	db, ctx, cleanup := setupBenchDB(b)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	// Create a payment order with gateway reference
	amount, err := domain.NewMoney("GBP", 10000)
	if err != nil {
		b.Fatalf("setup: NewMoney failed: %v", err)
	}
	po, err := domain.NewPaymentOrder("acc-123", "cred-ref", amount, "idem-key", "corr-001")
	if err != nil {
		b.Fatalf("setup: NewPaymentOrder failed: %v", err)
	}
	if err := repo.Create(ctx, po); err != nil {
		b.Fatalf("setup: Create failed: %v", err)
	}
	if err := po.Reserve("lien-123"); err != nil {
		b.Fatalf("setup: Reserve failed: %v", err)
	}
	if err := repo.Update(ctx, po); err != nil {
		b.Fatalf("setup: Update (Reserve) failed: %v", err)
	}
	if err := po.Execute("bench-gw-ref-001"); err != nil {
		b.Fatalf("setup: Execute failed: %v", err)
	}
	if err := repo.Update(ctx, po); err != nil {
		b.Fatalf("setup: Update (Execute) failed: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := repo.FindByGatewayReferenceID(ctx, "bench-gw-ref-001")
		if err != nil {
			b.Fatalf("FindByGatewayReferenceID failed: %v", err)
		}
	}
}

// BenchmarkRepository_Update benchmarks the Update operation with optimistic locking.
// This measures pure database UPDATE persistence performance.
//
// Note: Each iteration creates a fresh domain object but updates an existing database row.
// This isolates database UPDATE performance from state machine transition logic.
func BenchmarkRepository_Update(b *testing.B) {
	db, ctx, cleanup := setupBenchDB(b)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	// Pre-create a pool of payment orders for update testing.
	// Using a fixed pool size avoids OOM when b.N grows to millions during calibration.
	const poolSize = 1000

	// Store IDs and idempotency keys for recreating domain objects
	type orderInfo struct {
		id             string
		idempotencyKey string
	}
	orderInfos := make([]orderInfo, poolSize)

	for i := 0; i < poolSize; i++ {
		amount, err := domain.NewMoney("GBP", 10000)
		if err != nil {
			b.Fatalf("setup: NewMoney failed: %v", err)
		}
		idemKey := uuid.New().String()
		po, err := domain.NewPaymentOrder(
			"acc-123",
			"cred-ref",
			amount,
			idemKey,
			"corr-001",
		)
		if err != nil {
			b.Fatalf("setup: NewPaymentOrder failed: %v", err)
		}
		// Transition to RESERVED state for consistent baseline
		if err := po.Reserve("lien-" + uuid.New().String()); err != nil {
			b.Fatalf("setup: Reserve failed: %v", err)
		}
		if err := repo.Create(ctx, po); err != nil {
			b.Fatalf("setup: Create failed: %v", err)
		}
		orderInfos[i] = orderInfo{id: po.ID.String(), idempotencyKey: idemKey}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := i % poolSize
		info := orderInfos[idx]

		// Fetch fresh domain object from database to measure UPDATE performance
		po, err := repo.FindByID(ctx, uuid.MustParse(info.id))
		if err != nil {
			b.Fatalf("FindByID failed: %v", err)
		}

		// Transition to next state (RESERVED -> EXECUTING)
		// May fail if already executed (when b.N > poolSize and we cycle) - that's fine,
		// we're measuring UPDATE database performance, not state machine transitions
		_ = po.Execute("gw-ref-" + uuid.New().String())

		err = repo.Update(ctx, po)
		if err != nil {
			b.Fatalf("Update failed: %v", err)
		}
	}
}

// BenchmarkRepository_FindByDebtorAccountIDWithCursor benchmarks paginated listing.
// This tests cursor-based pagination with varying dataset sizes.
func BenchmarkRepository_FindByDebtorAccountIDWithCursor(b *testing.B) {
	benchmarks := []struct {
		name      string
		numOrders int
		pageSize  int
	}{
		{"10_orders_page_10", 10, 10},
		{"100_orders_page_50", 100, 50},
		{"1000_orders_page_100", 1000, 100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			db, ctx, cleanup := setupBenchDB(b)
			defer cleanup()

			repo := NewPaymentOrderRepository(db)

			// Pre-populate payment orders
			for i := 0; i < bm.numOrders; i++ {
				amount, err := domain.NewMoney("GBP", int64(1000+i))
				if err != nil {
					b.Fatalf("setup: NewMoney failed: %v", err)
				}
				po, err := domain.NewPaymentOrder(
					"acc-benchmark-cursor",
					"cred-ref",
					amount,
					uuid.New().String(),
					"corr-001",
				)
				if err != nil {
					b.Fatalf("setup: NewPaymentOrder failed: %v", err)
				}
				if err := repo.Create(ctx, po); err != nil {
					b.Fatalf("setup: Create failed: %v", err)
				}
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := repo.FindByDebtorAccountIDWithCursor(ctx, "acc-benchmark-cursor", bm.pageSize, Cursor{})
				if err != nil {
					b.Fatalf("FindByDebtorAccountIDWithCursor failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkRepository_StateTransitionSequence benchmarks a complete state transition sequence.
// This simulates the full lifecycle: INITIATED -> RESERVED -> EXECUTING -> COMPLETED.
func BenchmarkRepository_StateTransitionSequence(b *testing.B) {
	db, ctx, cleanup := setupBenchDB(b)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create
		amount, err := domain.NewMoney("GBP", 10000)
		if err != nil {
			b.Fatalf("NewMoney failed: %v", err)
		}
		po, err := domain.NewPaymentOrder(
			"acc-123",
			"cred-ref",
			amount,
			uuid.New().String(),
			"corr-001",
		)
		if err != nil {
			b.Fatalf("NewPaymentOrder failed: %v", err)
		}
		if err := repo.Create(ctx, po); err != nil {
			b.Fatalf("Create failed: %v", err)
		}

		// Reserve
		if err := po.Reserve("lien-" + uuid.New().String()); err != nil {
			b.Fatalf("Reserve failed: %v", err)
		}
		if err := repo.Update(ctx, po); err != nil {
			b.Fatalf("Update (Reserve) failed: %v", err)
		}

		// Execute
		if err := po.Execute("gw-ref-" + uuid.New().String()); err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
		if err := repo.Update(ctx, po); err != nil {
			b.Fatalf("Update (Execute) failed: %v", err)
		}

		// Complete
		if err := po.Complete(""); err != nil {
			b.Fatalf("Complete failed: %v", err)
		}
		if err := repo.Update(ctx, po); err != nil {
			b.Fatalf("Update (Complete) failed: %v", err)
		}
	}
}

// BenchmarkCursor_EncodeDecode benchmarks cursor encoding/decoding.
// This is used for pagination token handling.
func BenchmarkCursor_EncodeDecode(b *testing.B) {
	b.Run("Encode", func(b *testing.B) {
		cursor := Cursor{
			CreatedAt: benchTime,
			ID:        uuid.New(),
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_ = EncodeCursor(cursor)
		}
	})

	b.Run("Decode", func(b *testing.B) {
		cursor := Cursor{
			CreatedAt: benchTime,
			ID:        uuid.New(),
		}
		encoded := EncodeCursor(cursor)

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := DecodeCursor(encoded)
			if err != nil {
				b.Fatalf("DecodeCursor failed: %v", err)
			}
		}
	})

	b.Run("RoundTrip", func(b *testing.B) {
		cursor := Cursor{
			CreatedAt: benchTime,
			ID:        uuid.New(),
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			encoded := EncodeCursor(cursor)
			_, err := DecodeCursor(encoded)
			if err != nil {
				b.Fatalf("DecodeCursor failed: %v", err)
			}
		}
	})
}

// BenchmarkEntityConversion benchmarks domain<->entity conversion.
func BenchmarkEntityConversion(b *testing.B) {
	b.Run("toEntity", func(b *testing.B) {
		amount, err := domain.NewMoney("GBP", 10000)
		if err != nil {
			b.Fatalf("setup: NewMoney failed: %v", err)
		}
		po, err := domain.NewPaymentOrder("acc-123", "cred-ref", amount, "idem-key", "corr-001")
		if err != nil {
			b.Fatalf("setup: NewPaymentOrder failed: %v", err)
		}
		if err := po.Reserve("lien-123"); err != nil {
			b.Fatalf("setup: Reserve failed: %v", err)
		}
		if err := po.Execute("gw-ref-123"); err != nil {
			b.Fatalf("setup: Execute failed: %v", err)
		}
		if err := po.Complete(""); err != nil {
			b.Fatalf("setup: Complete failed: %v", err)
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_ = toEntity(po)
		}
	})

	b.Run("toDomain", func(b *testing.B) {
		amount, err := domain.NewMoney("GBP", 10000)
		if err != nil {
			b.Fatalf("setup: NewMoney failed: %v", err)
		}
		po, err := domain.NewPaymentOrder("acc-123", "cred-ref", amount, "idem-key", "corr-001")
		if err != nil {
			b.Fatalf("setup: NewPaymentOrder failed: %v", err)
		}
		if err := po.Reserve("lien-123"); err != nil {
			b.Fatalf("setup: Reserve failed: %v", err)
		}
		if err := po.Execute("gw-ref-123"); err != nil {
			b.Fatalf("setup: Execute failed: %v", err)
		}
		if err := po.Complete(""); err != nil {
			b.Fatalf("setup: Complete failed: %v", err)
		}
		entity := toEntity(po)

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := toDomain(entity)
			if err != nil {
				b.Fatalf("toDomain failed: %v", err)
			}
		}
	})
}
