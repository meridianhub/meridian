package cmd

// This file contains the instrument-checker registry adapter and CSV/proto
// conversion helpers used by the import command.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	csvadapter "github.com/meridianhub/meridian/cmd/position-tool/internal/adapters/csv"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/validation"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// csvRowToValidationRow converts a CSV row to a validation row.
func csvRowToValidationRow(csvRow *csvadapter.ImportRow) *validation.ImportRow {
	return &validation.ImportRow{
		LineNumber:     csvRow.LineNumber,
		AccountID:      csvRow.AccountID,
		InstrumentCode: csvRow.InstrumentCode,
		Amount:         csvRow.Amount,
		Timestamp:      csvRow.Timestamp,
		Attributes:     csvRow.Attributes,
	}
}

// instrumentCheckerRegistry adapts the InstrumentChecker to the quantity.InstrumentRegistry interface.
// This allows the CSV parser to use the gRPC-based instrument lookup from the validation package.
type instrumentCheckerRegistry struct {
	checker *validation.InstrumentChecker
}

// GetDefinition retrieves an instrument definition by code and version.
func (r *instrumentCheckerRegistry) GetDefinition(ctx context.Context, code string, version int32) (*quantity.InstrumentDefinition, error) {
	result, err := r.checker.Check(ctx, code, int(version))
	if err != nil {
		return nil, err
	}
	if !result.Exists {
		return nil, fmt.Errorf("%w: %s", ErrInstrumentNotFound, code)
	}
	return protoToQuantityDefinition(result.Definition), nil
}

// GetActiveDefinition retrieves the active version of an instrument.
func (r *instrumentCheckerRegistry) GetActiveDefinition(ctx context.Context, code string) (*quantity.InstrumentDefinition, error) {
	return r.GetDefinition(ctx, code, 0)
}

// ListActive returns all active instrument definitions.
func (r *instrumentCheckerRegistry) ListActive(_ context.Context) ([]*quantity.InstrumentDefinition, error) {
	return nil, fmt.Errorf("%w: ListActive", ErrOperationNotSupported)
}

// CreateDraft creates a new instrument definition in DRAFT status.
func (r *instrumentCheckerRegistry) CreateDraft(_ context.Context, _ *quantity.InstrumentDefinition) (*quantity.InstrumentDefinition, error) {
	return nil, fmt.Errorf("%w: CreateDraft", ErrOperationNotSupported)
}

// ActivateInstrument transitions an instrument from DRAFT to ACTIVE status.
func (r *instrumentCheckerRegistry) ActivateInstrument(_ context.Context, _ string, _ int32) error {
	return fmt.Errorf("%w: ActivateInstrument", ErrOperationNotSupported)
}

// DeprecateInstrument transitions an instrument to DEPRECATED status.
func (r *instrumentCheckerRegistry) DeprecateInstrument(_ context.Context, _ string, _ int32) error {
	return fmt.Errorf("%w: DeprecateInstrument", ErrOperationNotSupported)
}

// protoToQuantityDefinition converts a protobuf InstrumentDefinition to quantity.InstrumentDefinition.
func protoToQuantityDefinition(def *referencedatav1.InstrumentDefinition) *quantity.InstrumentDefinition {
	if def == nil {
		return nil
	}
	return &quantity.InstrumentDefinition{
		ID:                       def.Id,
		Code:                     def.Code,
		Version:                  def.Version,
		Dimension:                def.Dimension.String(),
		Precision:                def.Precision,
		Status:                   def.Status.String(),
		FungibilityKeyExpression: def.FungibilityKeyExpression,
		AttributeSchema:          def.AttributeSchema,
		DisplayName:              def.DisplayName,
		Description:              def.Description,
	}
}

// createDuplicateLookup creates a database lookup function for duplicate detection.
func createDuplicateLookup(pool *pgxpool.Pool) validation.DatabaseLookup {
	return func(ctx context.Context, measurementIDs []string) (map[string]bool, error) {
		if len(measurementIDs) == 0 {
			return make(map[string]bool), nil
		}

		// Query positions by reference_id (which stores measurement IDs)
		query := `SELECT DISTINCT reference_id::text FROM positions WHERE reference_id = ANY($1)`
		rows, err := pool.Query(ctx, query, measurementIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to query duplicates: %w", err)
		}
		defer rows.Close()

		exists := make(map[string]bool)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, fmt.Errorf("failed to scan duplicate: %w", err)
			}
			exists[id] = true
		}

		return exists, rows.Err()
	}
}
