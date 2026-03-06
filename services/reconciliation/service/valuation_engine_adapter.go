package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/meridianhub/meridian/shared/pkg/valuation"
)

// ValuationEngineAdapter wraps the shared valuation.Engine to satisfy the valuation.Engine
// interface within the reconciliation service. It adds structured logging for observability.
type ValuationEngineAdapter struct {
	engine valuation.Engine
	logger *slog.Logger
}

// NewValuationEngineAdapter creates a new adapter wrapping a real valuation.Engine.
func NewValuationEngineAdapter(engine valuation.Engine, logger *slog.Logger) *ValuationEngineAdapter {
	return &ValuationEngineAdapter{
		engine: engine,
		logger: logger,
	}
}

// Valuate delegates to the underlying Engine and logs the outcome.
func (a *ValuationEngineAdapter) Valuate(ctx context.Context, req *valuation.Request) (*valuation.Response, error) {
	a.logger.DebugContext(ctx, "valuating variance",
		"request_id", req.RequestID,
		"method_id", req.MethodID,
		"instrument", req.Quantity.InstrumentCode,
		"amount", req.Quantity.Amount.String(),
	)

	resp, err := a.engine.Valuate(ctx, req)
	if err != nil {
		a.logger.WarnContext(ctx, "valuation failed",
			"request_id", req.RequestID,
			"method_id", req.MethodID,
			"error", err,
		)
		return nil, fmt.Errorf("valuation engine error: %w", err)
	}

	a.logger.DebugContext(ctx, "valuation completed",
		"request_id", req.RequestID,
		"valued_amount", resp.ValuedAmount.Amount.String(),
		"output_instrument", resp.ValuedAmount.InstrumentCode,
		"cache_hit", resp.CacheHit,
	)

	return resp, nil
}
