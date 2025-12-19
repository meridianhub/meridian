package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/current-account/clients"
	cadomain "github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"google.golang.org/genproto/googleapis/type/money"
)

// benchLogger returns a no-op logger for benchmark tests
func benchLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// benchGatewayAccountConfig creates a gateway account config for benchmarks.
func benchGatewayAccountConfig() *config.GatewayAccountConfig {
	cfg, _ := config.NewGatewayAccountConfig(map[string]*config.GatewayAccountMapping{
		"mock": {
			GatewayID:       "mock",
			ContraAccountID: "GATEWAY-MOCK-NOSTRO-001",
			AccountType:     config.AccountTypeNostro,
		},
	})
	return cfg
}

// BenchmarkInitiatePaymentOrder benchmarks the InitiatePaymentOrder RPC.
// This measures the hot path for creating new payment orders including
// validation, domain object creation, and persistence.
func BenchmarkInitiatePaymentOrder(b *testing.B) {
	repo := NewMockRepository()
	mockCA := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-123",
			},
		},
	}
	mockGateway := &MockPaymentGateway{
		response: gateway.PaymentResponse{
			Status:             gateway.StatusAccepted,
			GatewayReferenceID: "gw-ref-123",
		},
	}

	svc, err := NewServiceWithConfig(Config{
		Repository:                repo,
		CurrentAccountClient:      mockCA,
		FinancialAccountingClient: &MockFinancialAccountingClient{},
		PaymentGateway:            mockGateway,
		GatewayAccountConfig:      benchGatewayAccountConfig(),
		Logger:                    benchLogger(),
		// Use fast retry config to avoid delays in benchmarks
		LienExecutionRetryConfig: &clients.RetryConfig{
			MaxRetries:      1,
			InitialInterval: 1,
			MaxInterval:     1,
			Multiplier:      1,
		},
	})
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := &pb.InitiatePaymentOrderRequest{
			DebtorAccountId:   "acc-123",
			CreditorReference: "cred-ref-001",
			Amount: &commonpb.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: uuid.New().String(), // Unique key per iteration
			},
			CorrelationId: "corr-001",
		}

		_, err := svc.InitiatePaymentOrder(ctx, req)
		if err != nil {
			b.Fatalf("InitiatePaymentOrder failed: %v", err)
		}
	}
}

// BenchmarkRetrievePaymentOrder benchmarks the RetrievePaymentOrder RPC.
// This measures the read path for fetching payment order details.
func BenchmarkRetrievePaymentOrder(b *testing.B) {
	repo := NewMockRepository()
	svc, err := NewService(repo)
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	// Create a payment order to retrieve
	amount, err := cadomain.NewMoney("GBP", 10000)
	if err != nil {
		b.Fatalf("setup: NewMoney failed: %v", err)
	}
	po, err := domain.NewPaymentOrder("acc-123", "cred-ref", amount, "idem-key", "corr-001")
	if err != nil {
		b.Fatalf("setup: NewPaymentOrder failed: %v", err)
	}
	ctx := context.Background()
	if err := repo.Create(ctx, po); err != nil {
		b.Fatalf("setup: Create failed: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := &pb.RetrievePaymentOrderRequest{
			PaymentOrderId: po.ID.String(),
		}

		_, err := svc.RetrievePaymentOrder(ctx, req)
		if err != nil {
			b.Fatalf("RetrievePaymentOrder failed: %v", err)
		}
	}
}

// BenchmarkListPaymentOrders benchmarks the ListPaymentOrders RPC.
// This measures pagination performance with varying result set sizes.
func BenchmarkListPaymentOrders(b *testing.B) {
	benchmarks := []struct {
		name      string
		numOrders int
		pageSize  int32
	}{
		{"10_orders_page_10", 10, 10},
		{"100_orders_page_50", 100, 50},
		{"1000_orders_page_100", 1000, 100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			repo := NewMockRepository()
			svc, err := NewService(repo)
			if err != nil {
				b.Fatalf("failed to create service: %v", err)
			}

			// Pre-populate payment orders
			ctx := context.Background()
			for i := 0; i < bm.numOrders; i++ {
				amount, err := cadomain.NewMoney("GBP", int64(1000+i))
				if err != nil {
					b.Fatalf("setup: NewMoney failed: %v", err)
				}
				po, err := domain.NewPaymentOrder(
					"acc-benchmark",
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
				req := &pb.ListPaymentOrdersRequest{
					DebtorAccountId: "acc-benchmark",
					Pagination: &commonpb.Pagination{
						PageSize: bm.pageSize,
					},
				}

				_, err := svc.ListPaymentOrders(ctx, req)
				if err != nil {
					b.Fatalf("ListPaymentOrders failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkUpdatePaymentOrder_Settled benchmarks the UpdatePaymentOrder RPC
// for SETTLED gateway callbacks.
//
// Note: This benchmark uses a pool of 1000 payment orders. After the first pass through
// the pool, orders transition to COMPLETED state and subsequent iterations measure the
// idempotent handling path (early return for already-completed orders). This is realistic
// as webhook callbacks may be delivered multiple times for the same payment.
func BenchmarkUpdatePaymentOrder_Settled(b *testing.B) {
	repo := NewMockRepository()
	mockCA := &MockCurrentAccountClient{
		executeLienResp: &currentaccountv1.ExecuteLienResponse{},
	}
	mockGateway := &MockPaymentGateway{}

	svc, err := NewServiceWithConfig(Config{
		Repository:                repo,
		CurrentAccountClient:      mockCA,
		FinancialAccountingClient: &MockFinancialAccountingClient{},
		PaymentGateway:            mockGateway,
		GatewayAccountConfig:      benchGatewayAccountConfig(),
		Logger:                    benchLogger(),
		LienExecutionRetryConfig: &clients.RetryConfig{
			MaxRetries:      1,
			InitialInterval: 1,
			MaxInterval:     1,
			Multiplier:      1,
		},
	})
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	ctx := context.Background()

	// Pre-create a pool of payment orders in EXECUTING state (ready for SETTLED callback).
	// Using a fixed pool size avoids OOM when b.N grows to millions during calibration.
	const poolSize = 1000
	paymentOrders := make([]*domain.PaymentOrder, poolSize)
	for i := 0; i < poolSize; i++ {
		amount, err := cadomain.NewMoney("GBP", 10000)
		if err != nil {
			b.Fatalf("setup: NewMoney failed: %v", err)
		}
		po, err := domain.NewPaymentOrder(
			"acc-123",
			"cred-ref",
			amount,
			uuid.New().String(),
			"corr-001",
		)
		if err != nil {
			b.Fatalf("setup: NewPaymentOrder failed: %v", err)
		}
		if err := po.Reserve("lien-" + uuid.New().String()); err != nil {
			b.Fatalf("setup: Reserve failed: %v", err)
		}
		if err := po.Execute("gw-ref-" + uuid.New().String()); err != nil {
			b.Fatalf("setup: Execute failed: %v", err)
		}
		if err := repo.Create(ctx, po); err != nil {
			b.Fatalf("setup: Create failed: %v", err)
		}
		paymentOrders[i] = po
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := &pb.UpdatePaymentOrderRequest{
			PaymentOrderId: paymentOrders[i%poolSize].ID.String(),
			GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
		}

		_, err := svc.UpdatePaymentOrder(ctx, req)
		if err != nil {
			b.Fatalf("UpdatePaymentOrder failed: %v", err)
		}
	}
}

// BenchmarkCancelPaymentOrder benchmarks the CancelPaymentOrder RPC.
//
// Note: This benchmark uses a pool of 1000 payment orders. After the first pass through
// the pool, orders transition to CANCELLED state and subsequent iterations measure the
// idempotent handling path (early return for already-cancelled orders). This is realistic
// as cancellation requests may be retried due to network issues.
func BenchmarkCancelPaymentOrder(b *testing.B) {
	repo := NewMockRepository()
	mockCA := &MockCurrentAccountClient{
		terminateLienResp: &currentaccountv1.TerminateLienResponse{},
	}
	mockGateway := &MockPaymentGateway{}

	svc, err := NewServiceWithConfig(Config{
		Repository:                repo,
		CurrentAccountClient:      mockCA,
		FinancialAccountingClient: &MockFinancialAccountingClient{},
		PaymentGateway:            mockGateway,
		GatewayAccountConfig:      benchGatewayAccountConfig(),
		Logger:                    benchLogger(),
	})
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	ctx := context.Background()

	// Pre-create a pool of payment orders in RESERVED state (cancellable).
	// Using a fixed pool size avoids OOM when b.N grows to millions during calibration.
	const poolSize = 1000
	paymentOrders := make([]*domain.PaymentOrder, poolSize)
	for i := 0; i < poolSize; i++ {
		amount, err := cadomain.NewMoney("GBP", 10000)
		if err != nil {
			b.Fatalf("setup: NewMoney failed: %v", err)
		}
		po, err := domain.NewPaymentOrder(
			"acc-123",
			"cred-ref",
			amount,
			uuid.New().String(),
			"corr-001",
		)
		if err != nil {
			b.Fatalf("setup: NewPaymentOrder failed: %v", err)
		}
		if err := po.Reserve("lien-" + uuid.New().String()); err != nil {
			b.Fatalf("setup: Reserve failed: %v", err)
		}
		if err := repo.Create(ctx, po); err != nil {
			b.Fatalf("setup: Create failed: %v", err)
		}
		paymentOrders[i] = po
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := &pb.CancelPaymentOrderRequest{
			PaymentOrderId:     paymentOrders[i%poolSize].ID.String(),
			CancellationReason: "benchmark cancellation",
			CancelledBy:        "benchmark-user",
		}

		_, err := svc.CancelPaymentOrder(ctx, req)
		if err != nil {
			b.Fatalf("CancelPaymentOrder failed: %v", err)
		}
	}
}

// BenchmarkMoneyConversion benchmarks the proto-to-domain money conversion.
// This is a frequently used helper in the hot path.
func BenchmarkMoneyConversion(b *testing.B) {
	b.Run("protoToMoney", func(b *testing.B) {
		amount := &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        12345,
				Nanos:        670000000, // 0.67
			},
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := protoToMoney(amount)
			if err != nil {
				b.Fatalf("protoToMoney failed: %v", err)
			}
		}
	})

	b.Run("toMoneyAmount", func(b *testing.B) {
		amount, err := cadomain.NewMoney("GBP", 1234567)
		if err != nil {
			b.Fatalf("setup: NewMoney failed: %v", err)
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_ = toMoneyAmount(amount)
		}
	})
}

// BenchmarkToProto benchmarks the domain-to-proto conversion.
// This is called on every response path.
func BenchmarkToProto(b *testing.B) {
	amount, err := cadomain.NewMoney("GBP", 10000)
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
		_ = toProto(po)
	}
}
