// Package messaging provides Kafka consumer adapters for event-driven communication.
package messaging

import (
	"context"
	"fmt"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/internal/financial-accounting/service"
	"github.com/meridianhub/meridian/internal/platform/kafka"
	"google.golang.org/protobuf/proto"
)

// DepositConsumer consumes DepositEvent messages from Kafka and processes them
// through the PostingService to create double-entry ledger postings.
type DepositConsumer struct {
	consumer       *kafka.ProtoConsumer
	postingService *service.PostingService
}

// NewDepositConsumer creates a Kafka consumer for DepositEvent messages.
// It connects to Kafka using the provided configuration and sets up a handler
// that converts DepositEvents into PostingService commands.
//
// Parameters:
// - config: Kafka consumer configuration (bootstrap servers, group ID, etc.)
// - postingService: Service that creates ledger postings
//
// Returns an error if the consumer cannot be initialized.
func NewDepositConsumer(config kafka.ConsumerConfig, postingService *service.PostingService) (*DepositConsumer, error) {
	dc := &DepositConsumer{
		postingService: postingService,
	}

	// Message factory creates new DepositEvent instances for deserialization
	msgFactory := func() proto.Message {
		return &eventsv1.DepositEvent{}
	}

	// Handler converts Kafka messages to service commands
	handler := func(ctx context.Context, _ []byte, msg proto.Message) error {
		return dc.handleDepositEvent(ctx, msg.(*eventsv1.DepositEvent))
	}

	consumer, err := kafka.NewProtoConsumer(config, msgFactory, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to create deposit consumer: %w", err)
	}

	dc.consumer = consumer
	return dc, nil
}

// handleDepositEvent processes a single DepositEvent by converting it to
// the PostingService format and creating double-entry ledger postings.
func (dc *DepositConsumer) handleDepositEvent(ctx context.Context, event *eventsv1.DepositEvent) error {
	// Convert proto timestamp to time.Time
	valueDate := event.ValueDate.AsTime()

	// Convert proto currency enum to ISO code (e.g., CURRENCY_GBP -> GBP)
	currencyCode := convertCurrencyToISO(event.Currency)

	// Create service event
	depositEvent := service.DepositEvent{
		AccountID:     event.AccountId,
		AmountCents:   event.AmountCents,
		Currency:      currencyCode,
		CorrelationID: event.CorrelationId,
		ValueDate:     valueDate,
	}

	// Process through posting service
	if err := dc.postingService.ProcessDeposit(ctx, depositEvent); err != nil {
		return fmt.Errorf("failed to process deposit: %w", err)
	}

	return nil
}

// convertCurrencyToISO converts proto Currency enum to ISO 4217 code string.
// Example: CURRENCY_GBP -> "GBP"
func convertCurrencyToISO(currency commonv1.Currency) string {
	switch currency {
	case commonv1.Currency_CURRENCY_UNSPECIFIED:
		return ""
	case commonv1.Currency_CURRENCY_GBP:
		return "GBP"
	case commonv1.Currency_CURRENCY_USD:
		return "USD"
	case commonv1.Currency_CURRENCY_EUR:
		return "EUR"
	case commonv1.Currency_CURRENCY_JPY:
		return "JPY"
	case commonv1.Currency_CURRENCY_CHF:
		return "CHF"
	case commonv1.Currency_CURRENCY_CAD:
		return "CAD"
	case commonv1.Currency_CURRENCY_AUD:
		return "AUD"
	default:
		return ""
	}
}

// Start begins consuming DepositEvent messages from the specified topics.
// This method blocks until Stop() is called or an error occurs.
func (dc *DepositConsumer) Start(topics []string) error {
	if err := dc.consumer.Subscribe(topics); err != nil {
		return fmt.Errorf("failed to subscribe to topics: %w", err)
	}
	return nil
}

// Stop gracefully stops the consumer.
func (dc *DepositConsumer) Stop() {
	dc.consumer.Stop()
}

// Close closes the consumer and releases resources.
func (dc *DepositConsumer) Close() error {
	if err := dc.consumer.Close(); err != nil {
		return fmt.Errorf("failed to close consumer: %w", err)
	}
	return nil
}
