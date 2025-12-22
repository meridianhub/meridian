package worker

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNewProvisioningWorker_Success(t *testing.T) {
	// Setup dependencies
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second

	// Create worker
	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, worker)
	assert.Equal(t, repo, worker.repo)
	assert.Equal(t, prov, worker.provisioner)
	assert.Equal(t, pollInterval, worker.pollInterval)
	assert.Equal(t, logger, worker.logger)
	assert.NotNil(t, worker.done)
}

func TestNewProvisioningWorker_NilRepository(t *testing.T) {
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(nil, prov, pollInterval, logger)

	assert.ErrorIs(t, err, ErrNilRepository)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilProvisioner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(repo, nil, pollInterval, logger)

	assert.ErrorIs(t, err, ErrNilProvisioner)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilLogger(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, nil)

	assert.ErrorIs(t, err, ErrNilLogger)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_ZeroPollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	worker, err := NewProvisioningWorker(repo, prov, 0, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NegativePollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	worker, err := NewProvisioningWorker(repo, prov, -5*time.Second, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}
