package db_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// testEntity is a simple GORM model for testing
type testEntity struct {
	ID   uint   `gorm:"primarykey"`
	Name string `gorm:"column:name"`
}

func (testEntity) TableName() string { return "test_entities" }

func newMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { mockDB.Close() })

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	return gormDB, mock
}

func TestTenantGuard_BlocksQueryWithoutTenantScope(t *testing.T) {
	t.Parallel()
	gormDB, _ := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	// Query without tenant scope — should be blocked
	var entities []testEntity
	err = gormDB.WithContext(context.Background()).Find(&entities).Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

func TestTenantGuard_BlocksCreateWithoutTenantScope(t *testing.T) {
	t.Parallel()
	gormDB, _ := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	entity := testEntity{Name: "test"}
	err = gormDB.WithContext(context.Background()).Create(&entity).Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

func TestTenantGuard_BlocksUpdateWithoutTenantScope(t *testing.T) {
	t.Parallel()
	gormDB, _ := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	err = gormDB.WithContext(context.Background()).
		Model(&testEntity{}).
		Where("id = ?", 1).
		Update("name", "updated").Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

func TestTenantGuard_BlocksDeleteWithoutTenantScope(t *testing.T) {
	t.Parallel()
	gormDB, _ := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	err = gormDB.WithContext(context.Background()).
		Where("id = ?", 1).
		Delete(&testEntity{}).Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

func TestTenantGuard_AllowsQueryWithTenantScope(t *testing.T) {
	t.Parallel()
	gormDB, mock := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// WithGormTenantScope requires an active transaction
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("org_acme_bank").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT \* FROM "test_entities"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectCommit()

	tx := gormDB.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)

	scopedDB, err := db.WithGormTenantScope(ctx, tx)
	require.NoError(t, err)

	var entities []testEntity
	err = scopedDB.Find(&entities).Error
	require.NoError(t, err)

	require.NoError(t, tx.Commit().Error)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTenantGuard_AllowsCreateWithTenantScope(t *testing.T) {
	t.Parallel()
	gormDB, mock := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// WithGormTenantScope requires an active transaction
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("org_acme_bank").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`INSERT INTO "test_entities"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	tx := gormDB.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)

	scopedDB, err := db.WithGormTenantScope(ctx, tx)
	require.NoError(t, err)

	entity := testEntity{Name: "test"}
	err = scopedDB.Create(&entity).Error
	require.NoError(t, err)

	require.NoError(t, tx.Commit().Error)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTenantGuard_AllowsWithinTenantTransaction(t *testing.T) {
	t.Parallel()
	gormDB, mock := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("org_acme_bank").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT \* FROM "test_entities"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectCommit()

	var entities []testEntity
	err = db.WithGormTenantTransaction(ctx, gormDB, func(tx *gorm.DB) error {
		return tx.Find(&entities).Error
	})
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTenantGuard_AllowsBypassForMigrations(t *testing.T) {
	t.Parallel()
	gormDB, mock := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	// Bypass context should allow queries without tenant scope
	ctx := db.WithTenantGuardBypass(context.Background())

	mock.ExpectQuery(`SELECT \* FROM "test_entities"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

	var entities []testEntity
	err = gormDB.WithContext(ctx).Find(&entities).Error
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTenantGuard_BlocksRawWithoutTenantScope(t *testing.T) {
	t.Parallel()
	gormDB, _ := newMockGormDB(t)

	err := gormDB.Use(db.NewTenantGuard())
	require.NoError(t, err)

	// Raw exec without tenant scope — should be blocked
	err = gormDB.WithContext(context.Background()).
		Exec("INSERT INTO test_entities (name) VALUES (?)", "test").Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

func TestTenantScope_RejectsNonTransaction(t *testing.T) {
	t.Parallel()
	gormDB, _ := newMockGormDB(t)

	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Calling WithGormTenantScope outside a transaction should fail
	_, err := db.WithGormTenantScope(ctx, gormDB.WithContext(ctx))
	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequiresTransaction)
}

func TestTenantGuard_PluginName(t *testing.T) {
	t.Parallel()
	guard := db.NewTenantGuard()
	assert.Equal(t, "meridian:tenant_guard", guard.Name())
}
