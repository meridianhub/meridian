package service

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// currentAccountPositionKeepingInitiateLog creates a position log entry for a withdrawal/deposit.
//
// Required params:
//   - account_id: string - The account identifier
//   - amount: decimal.Decimal - The transaction amount
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - direction: string - "DEBIT" or "CREDIT"
//   - transaction_id: string - The saga transaction ID
//
// Returns:
//
//	map[string]any{
//	  "log_id":  string - The created position log ID
//	  "version": int64  - The position log version
//	  "status":  string - "INITIATED"
//	}
func currentAccountPositionKeepingInitiateLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.position_keeping.initiate_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Extract required parameters
	// Accept either position_id (schema primary) or account_id (legacy alias)
	accountID, ok := params["position_id"].(string)
	if !ok || accountID == "" {
		accountID, ok = params["account_id"].(string)
		if !ok || accountID == "" {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: position_id or account_id", errMissingParameter))
		}
	}

	amount, err := requireDecimal(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Accept instrument_code (preferred) or currency (deprecated alias)
	currency := optionalString(params, "instrument_code")
	if currency == "" {
		currency = optionalString(params, "currency")
	}
	if currency == "" {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: instrument_code", errMissingParameter))
	}

	direction, err := requireString(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Validate direction
	var pbDirection commonpb.PostingDirection
	switch direction {
	case directionDebit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_DEBIT
	case directionCredit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_CREDIT
	default:
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", errInvalidDirection, direction))
	}

	// Determine description based on direction
	description := fmt.Sprintf("Deposit to account %s", accountID)
	idempKeyPrefix := sagaTypeDeposit
	if direction == directionDebit {
		description = fmt.Sprintf("Withdrawal from account %s", accountID)
		idempKeyPrefix = sagaTypeWithdrawal
	}

	// Extract optional valuation_analysis parameter
	var attributes map[string]string
	if valuationAnalysis, ok := params["valuation_analysis"]; ok && valuationAnalysis != nil {
		// Marshal valuation_analysis to JSON for storage in attributes
		bytes, marshalErr := json.Marshal(valuationAnalysis)
		if marshalErr != nil {
			deps.Logger.Warn("failed to marshal valuation_analysis",
				"error", marshalErr,
				"transaction_id", transactionID)
		} else {
			attributes = map[string]string{
				"valuation_analysis": string(bytes),
			}
			deps.Logger.Debug("including valuation_analysis in position attributes",
				"transaction_id", transactionID,
				"analysis_size", len(bytes))
		}
	}

	deps.Logger.Info("executing position_keeping.initiate_log",
		"account_id", accountID,
		"transaction_id", transactionID,
		"direction", direction,
		"has_valuation_analysis", attributes != nil)

	// Create proto amount
	protoAmount := decimalToMoneyAmount(amount, currency)

	// Build transaction log entry
	initialEntry := &positionkeepingv1.TransactionLogEntry{
		EntryId:       uuid.New().String(),
		TransactionId: transactionID,
		AccountId:     accountID,
		Amount:        protoAmount,
		Direction:     pbDirection,
		Timestamp:     timestamppb.Now(),
		Description:   description,
		Attributes:    attributes,
	}

	// Call Position Keeping service
	if deps.PosKeepingClient == nil {
		return nil, wrapHandlerError(handlerName, errPosKeepingClientNil)
	}
	resp, err := deps.PosKeepingClient.InitiateFinancialPositionLog(ctx,
		&positionkeepingv1.InitiateFinancialPositionLogRequest{
			AccountId:    accountID,
			InitialEntry: initialEntry,
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("%s-%s-%s", idempKeyPrefix, accountID, transactionID),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("position_keeping", "initiate_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to log position: %w", err))
	}
	if resp.Log == nil {
		caobservability.RecordExternalServiceError("position_keeping", "initiate_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: transaction %s", errNilPositionLog, transactionID))
	}

	deps.Logger.Info("position_keeping.initiate_log completed",
		"log_id", resp.Log.LogId,
		"version", resp.Log.Version,
		"transaction_id", transactionID)

	return map[string]any{
		"log_id":  resp.Log.LogId,
		"version": resp.Log.Version,
		"status":  "INITIATED",
	}, nil
}

// currentAccountPositionKeepingCancelLog cancels a position log entry (compensation).
//
// Required params:
//   - log_id: string - The position log ID to cancel
//   - version: int64 - The position log version
//   - transaction_id: string - The saga transaction ID
//   - account_id: string - The account ID
//   - direction: string - "DEBIT" or "CREDIT" (for idempotency key)
func currentAccountPositionKeepingCancelLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.position_keeping.cancel_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	logID, err := requireString(params, "log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	version, err := requireInt64(params, "version")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireString(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	direction, err := requireString(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	idempKeyPrefix := sagaTypeDeposit
	sagaType := sagaTypeDeposit
	if direction == directionDebit {
		idempKeyPrefix = sagaTypeWithdrawal
		sagaType = sagaTypeWithdrawal
	}

	if deps.PosKeepingClient == nil {
		return nil, wrapHandlerError(handlerName, errPosKeepingClientNil)
	}

	deps.Logger.Info("compensating position_keeping.cancel_log",
		"log_id", logID,
		"version", version,
		"transaction_id", transactionID)

	_, err = deps.PosKeepingClient.UpdateFinancialPositionLog(ctx,
		&positionkeepingv1.UpdateFinancialPositionLogRequest{
			LogId:   logID,
			Version: version,
			StatusUpdate: &positionkeepingv1.StatusTracking{
				CurrentStatus:   commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
				StatusUpdatedAt: timestamppb.Now(),
				StatusReason:    fmt.Sprintf("Saga compensation for failed %s transaction %s", sagaType, transactionID),
			},
			AuditEntry: &positionkeepingv1.AuditTrailEntry{
				AuditId:   uuid.New().String(),
				Timestamp: timestamppb.Now(),
				UserId:    "system",
				Action:    "saga_compensation",
				Details:   fmt.Sprintf("Cancelled position log due to %s saga failure for transaction %s", sagaType, transactionID),
			},
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("compensate-%s-%s-%s", idempKeyPrefix, accountID, transactionID),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("position_keeping", "compensate_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to compensate position log: %w", err))
	}

	caobservability.RecordSagaCompensation(sagaType, "log_position")

	deps.Logger.Info("position_keeping.cancel_log completed",
		"log_id", logID)

	return map[string]any{
		"log_id": logID,
		"status": "CANCELLED",
	}, nil
}
