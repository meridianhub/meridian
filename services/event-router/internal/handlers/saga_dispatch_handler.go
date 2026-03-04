// Package handlers provides event handler implementations for the event-router.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/event-router/domain"
	sagaidempotency "github.com/meridianhub/meridian/services/event-router/internal/idempotency"
	"github.com/meridianhub/meridian/services/event-router/internal/registry"
	sharedidempotency "github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/protobuf/proto"
)

// ErrHandlerNotInitialized is returned when Handle is called on a handler with nil dependencies.
var ErrHandlerNotInitialized = errors.New("saga dispatch handler is not properly initialized")

const (
	// defaultMaxChainDepth is the maximum saga chain depth before events are dropped.
	defaultMaxChainDepth = 10

	// chainDepthHeader is the metadata key carrying the current chain depth.
	chainDepthHeader = "x-chain-depth"

	// correlationIDHeader is the metadata key carrying the correlation ID.
	correlationIDHeader = "x-correlation-id"
)

// idempotencyStore is the interface for idempotency-protected saga dispatch.
type idempotencyStore interface {
	Execute(ctx context.Context, sagaName, correlationID string, fn sagaidempotency.DispatchFunc) (*sharedidempotency.ExecuteResult, error)
}

// SagaDispatchHandler evaluates CEL filters against incoming events and triggers
// matching sagas via the SagaTrigger port. It implements domain.EventHandler.
type SagaDispatchHandler struct {
	registry         *registry.SagaRegistry
	sagaTrigger      domain.SagaTrigger
	maxChainDepth    int
	logger           *slog.Logger
	idempotencyStore idempotencyStore
}

// Option configures a SagaDispatchHandler.
type Option func(*SagaDispatchHandler)

// WithMaxChainDepth sets the maximum allowed chain depth. Events with a chain
// depth >= this value are dropped with a warning log. Non-positive values are
// ignored and the default is kept to prevent accidentally disabling dispatch.
func WithMaxChainDepth(depth int) Option {
	return func(h *SagaDispatchHandler) {
		if depth > 0 {
			h.maxChainDepth = depth
		}
	}
}

// WithLogger sets the structured logger. A nil logger is ignored.
func WithLogger(logger *slog.Logger) Option {
	return func(h *SagaDispatchHandler) {
		if logger != nil {
			h.logger = logger
		}
	}
}

// WithIdempotencyStore sets the idempotency store used to deduplicate saga dispatches.
// When set, each saga trigger is wrapped with idempotency protection keyed on
// (sagaName, correlationID). A nil store is ignored.
func WithIdempotencyStore(store idempotencyStore) Option {
	return func(h *SagaDispatchHandler) {
		if store != nil {
			h.idempotencyStore = store
		}
	}
}

// NewSagaDispatchHandler creates a handler that dispatches events to sagas
// registered in the provided registry.
func NewSagaDispatchHandler(reg *registry.SagaRegistry, trigger domain.SagaTrigger, opts ...Option) *SagaDispatchHandler {
	h := &SagaDispatchHandler{
		registry:      reg,
		sagaTrigger:   trigger,
		maxChainDepth: defaultMaxChainDepth,
		logger:        slog.Default().With("component", "saga_dispatch_handler"),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Handle processes a single event by looking up applicable sagas for the channel,
// evaluating each saga's CEL filter, and triggering matching sagas.
//
// Chain depth enforcement: if the metadata carries an x-chain-depth value >= the
// configured maximum, the event is dropped silently (with a warning log) to
// prevent infinite saga chains.
//
// Filter evaluation errors cause the individual saga to be skipped (with a
// warning log) while other sagas continue processing. Trigger errors are
// returned immediately as they indicate infrastructure failures.
//
// When an idempotency store is configured, each saga trigger is deduplicated
// by (sagaName, correlationID). Duplicate dispatches are logged and skipped.
// ErrOperationInProgress is logged and skipped (another worker is processing).
func (h *SagaDispatchHandler) Handle(ctx context.Context, channel string, event proto.Message, metadata map[string]string) error {
	if h.registry == nil || h.sagaTrigger == nil {
		return ErrHandlerNotInitialized
	}

	// Convert event to input_data (also validates event is non-nil).
	inputData, err := saga.EventToInputData(event, metadata)
	if err != nil {
		return fmt.Errorf("convert event to input_data: %w", err)
	}

	// Check chain depth.
	depth := extractChainDepth(metadata)
	if depth >= h.maxChainDepth {
		h.logger.WarnContext(ctx, "chain depth exceeded, dropping event",
			"channel", channel,
			"chain_depth", depth,
			"max_chain_depth", h.maxChainDepth,
		)
		return nil
	}

	// Look up applicable sagas for this channel.
	sagas := h.registry.GetApplicableSagas(channel)
	if len(sagas) == 0 {
		return nil
	}

	// Build the CEL activation map for filter evaluation.
	// CEL event filter environment expects "event" (dyn) and "metadata" (map[string]string).
	activation := map[string]any{
		"event":    inputData["event"],
		"metadata": metadata,
	}

	idempotencyKey := extractCorrelationID(metadata)
	if idempotencyKey == "" {
		idempotencyKey = uuid.New().String()
		h.logger.WarnContext(ctx, "no correlation ID in metadata, generated UUID as idempotency key — Kafka redelivery may cause duplicate saga executions",
			"channel", channel,
			"generated_key", idempotencyKey,
		)
	}

	for _, cs := range sagas {
		sagaName := cs.Definition.GetName()

		// If no filter, the saga always matches.
		if cs.FilterProgram == nil {
			if err := h.dispatchSaga(ctx, sagaName, channel, inputData, idempotencyKey); err != nil {
				return err
			}
			continue
		}

		// Evaluate CEL filter.
		out, _, evalErr := cs.FilterProgram.Eval(activation)
		if evalErr != nil {
			h.logger.WarnContext(ctx, "CEL filter evaluation error, skipping saga",
				"saga_name", sagaName,
				"channel", channel,
				"error", evalErr,
			)
			continue
		}

		matched, ok := out.Value().(bool)
		if !ok {
			h.logger.WarnContext(ctx, "CEL filter returned non-boolean, skipping saga",
				"saga_name", sagaName,
				"channel", channel,
				"result_type", fmt.Sprintf("%T", out.Value()),
			)
			continue
		}

		if !matched {
			h.logger.DebugContext(ctx, "CEL filter did not match, skipping saga",
				"saga_name", sagaName,
				"channel", channel,
			)
			continue
		}

		if err := h.dispatchSaga(ctx, sagaName, channel, inputData, idempotencyKey); err != nil {
			return err
		}
	}

	return nil
}

// dispatchSaga triggers a single saga, optionally wrapping with idempotency protection.
func (h *SagaDispatchHandler) dispatchSaga(
	ctx context.Context,
	sagaName, channel string,
	inputData map[string]any,
	correlationID string,
) error {
	if h.idempotencyStore == nil {
		// No idempotency store — trigger directly.
		if _, err := h.sagaTrigger.TriggerSaga(ctx, sagaName, inputData, correlationID); err != nil {
			return fmt.Errorf("trigger saga %q: %w", sagaName, err)
		}
		h.logger.DebugContext(ctx, "saga triggered",
			"saga_name", sagaName,
			"channel", channel,
			"correlation_id", correlationID,
		)
		return nil
	}

	// Idempotency-protected dispatch.
	result, err := h.idempotencyStore.Execute(ctx, sagaName, correlationID, func(ctx context.Context) error {
		if _, triggerErr := h.sagaTrigger.TriggerSaga(ctx, sagaName, inputData, correlationID); triggerErr != nil {
			return fmt.Errorf("trigger saga %q: %w", sagaName, triggerErr)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sharedidempotency.ErrOperationInProgress) {
			h.logger.InfoContext(ctx, "saga dispatch in progress by another worker, skipping",
				"saga_name", sagaName,
				"channel", channel,
				"correlation_id", correlationID,
			)
			return nil
		}
		return fmt.Errorf("idempotent dispatch of saga %q: %w", sagaName, err)
	}

	if result.FromCache {
		h.logger.InfoContext(ctx, "saga already dispatched (idempotent skip)",
			"saga_name", sagaName,
			"channel", channel,
			"correlation_id", correlationID,
		)
		return nil
	}

	h.logger.DebugContext(ctx, "saga triggered",
		"saga_name", sagaName,
		"channel", channel,
		"correlation_id", correlationID,
	)
	return nil
}

// extractChainDepth reads the chain depth from metadata, returning 0 if absent,
// unparseable, or negative.
func extractChainDepth(metadata map[string]string) int {
	raw, ok := metadata[chainDepthHeader]
	if !ok {
		return 0
	}
	depth, err := strconv.Atoi(raw)
	if err != nil || depth < 0 {
		return 0
	}
	return depth
}

// extractCorrelationID reads the correlation ID from metadata.
// Returns empty string if absent — caller is responsible for fallback and logging.
func extractCorrelationID(metadata map[string]string) string {
	if id, ok := metadata[correlationIDHeader]; ok && id != "" {
		return id
	}
	return ""
}
