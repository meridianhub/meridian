package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDbWrapper_Ping_Success(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectPing()

	w := &dbWrapper{db: db}
	err = w.Ping(context.Background())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDbWrapper_Ping_Error(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectPing().WillReturnError(errors.New("connection refused"))

	w := &dbWrapper{db: db}
	err = w.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDbWrapper_Stats_ReturnsDBStats(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	w := &dbWrapper{db: db}
	stats := w.Stats()

	// A freshly opened mock DB has zero stats - just verify the type and no panic
	assert.IsType(t, sql.DBStats{}, stats)
}

func TestDbWrapper_Stats_MaxOpenConns(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	db.SetMaxOpenConns(10)

	w := &dbWrapper{db: db}
	stats := w.Stats()

	assert.Equal(t, 10, stats.MaxOpenConnections)
}
