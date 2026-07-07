package service_test

import (
	"context"

	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"gorm.io/gorm"
)

// UpdateWithOutbox test shims delegate to each mock's Update and then run the
// outbox callback in the same (mock) transaction, mirroring the production
// transactional-outbox contract. Defining them centrally keeps the individual
// mock definitions focused on their primary behavior.

func (m *updateFailRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := m.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}

func (m *findCountRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := m.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}

func (m *updateAfterRunningRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := m.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}

func (m *listRunRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := m.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}

func (m *testRunRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := m.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}

func (m *mockSettlementRunRepo) UpdateWithOutbox(ctx context.Context, run *domain.SettlementRun, postFn func(tx *gorm.DB) error) error {
	if err := m.Update(ctx, run); err != nil {
		return err
	}
	if postFn != nil {
		return postFn(nil)
	}
	return nil
}
