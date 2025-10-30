package messaging

import (
	"context"
	"testing"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/internal/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/internal/financial-accounting/service"
	"github.com/meridianhub/meridian/internal/platform/kafka"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestServices(t *testing.T) *service.PostingService {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	err = db.AutoMigrate(&persistence.LedgerPostingEntity{}, &persistence.FinancialBookingLogEntity{})
	if err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	repo := persistence.NewLedgerRepository(db)
	return service.NewPostingService(repo)
}

func TestNewDepositConsumer(t *testing.T) {
	postingService := setupTestServices(t)

	tests := []struct {
		name    string
		config  kafka.ConsumerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
				ClientID:         "test-consumer",
			},
			wantErr: false,
		},
		{
			name: "missing bootstrap servers",
			config: kafka.ConsumerConfig{
				GroupID: "test-group",
			},
			wantErr: true,
		},
		{
			name: "missing group ID",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer, err := NewDepositConsumer(tt.config, postingService)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDepositConsumer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && consumer != nil {
				defer func() {
					_ = consumer.Close()
				}()
			}
		})
	}
}

func TestDepositConsumer_HandleDepositEvent(t *testing.T) {
	postingService := setupTestServices(t)

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	tests := []struct {
		name    string
		event   *eventsv1.DepositEvent
		wantErr bool
	}{
		{
			name: "valid deposit event",
			event: &eventsv1.DepositEvent{
				AccountId:     "ACC-123",
				AmountCents:   10000,
				Currency:      commonv1.Currency_CURRENCY_GBP,
				CorrelationId: "deposit-001",
				ValueDate:     timestamppb.Now(),
				Timestamp:     timestamppb.Now(),
			},
			wantErr: false,
		},
		{
			name: "zero amount",
			event: &eventsv1.DepositEvent{
				AccountId:     "ACC-456",
				AmountCents:   0,
				Currency:      commonv1.Currency_CURRENCY_GBP,
				CorrelationId: "deposit-002",
				ValueDate:     timestamppb.Now(),
				Timestamp:     timestamppb.Now(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := consumer.handleDepositEvent(ctx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleDepositEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
