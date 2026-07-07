package service

import (
	"context"

	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"gorm.io/gorm"
)

// UpdateWithOutbox test shims delegate to each mock's Update and then run the
// outbox callback in the same (mock) transaction, mirroring the production
// transactional-outbox contract. Defining them centrally keeps the individual
// mock definitions focused on their primary behavior.

func (m *vdRunRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := m.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}

func (r *gormRunRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := r.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}
