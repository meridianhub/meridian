package valuationfeature

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
)

// ProductTypeSeeder seeds ValuationFeature records from a product type's
// ValuationMethodTemplate list at account creation time.
// It uses upsert semantics so saga retries are safe.
type ProductTypeSeeder struct {
	repo *Repository
}

// NewProductTypeSeeder creates a new ProductTypeSeeder.
func NewProductTypeSeeder(repo *Repository) *ProductTypeSeeder {
	return &ProductTypeSeeder{repo: repo}
}

// SeedFromProductType seeds ValuationFeature records for a new account based on
// the product type's ValuationMethodTemplate list.
//
// Only ACTIVE templates are seeded. DRAFT and DEPRECATED templates are skipped.
// Same-dimension conversions (resolved via DefaultConversionMethodID at runtime)
// do NOT need seeded features — they are resolved from the product type at eval time.
//
// Uses INSERT ... ON CONFLICT DO NOTHING for idempotency: calling this method
// multiple times for the same (accountID, instrumentCode) pair is safe.
func (s *ProductTypeSeeder) SeedFromProductType(
	ctx context.Context,
	accountID uuid.UUID,
	productType *accounttype.Definition,
	now time.Time,
) error {
	maxTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

	for _, tmpl := range productType.ValuationMethods {
		if tmpl.Status != accounttype.StatusActive {
			continue
		}

		feature := &ValuationFeature{
			ID:                     uuid.New(),
			AccountID:              accountID,
			InstrumentCode:         tmpl.InputInstrument,
			ValuationMethodID:      tmpl.ValuationMethodID,
			ValuationMethodVersion: tmpl.ValuationMethodVersion,
			Parameters:             tmpl.Parameters,
			LifecycleStatus:        LifecycleStatusActive,
			ValidFrom:              now,
			ValidTo:                maxTime,
			CreatedAt:              now,
			CreatedBy:              "system",
			UpdatedAt:              now,
			UpdatedBy:              "system",
			Version:                1,
		}

		if err := s.repo.UpsertFeature(ctx, feature); err != nil {
			return fmt.Errorf("seed valuation feature %s for account %s: %w",
				tmpl.InputInstrument, accountID, err)
		}
	}

	return nil
}
