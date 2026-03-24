package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultTxOptions(t *testing.T) {
	opts := DefaultTxOptions()
	assert.Equal(t, Serializable, opts.Isolation)
	assert.False(t, opts.ReadOnly)
}

func TestToSQLOptions(t *testing.T) {
	tests := []struct {
		name             string
		opts             *TxOptions
		expectedLevel    sql.IsolationLevel
		expectedReadOnly bool
	}{
		{
			"serializable",
			&TxOptions{Isolation: Serializable, ReadOnly: false},
			sql.LevelSerializable,
			false,
		},
		{
			"read_committed",
			&TxOptions{Isolation: ReadCommitted, ReadOnly: false},
			sql.LevelReadCommitted,
			false,
		},
		{
			"repeatable_read",
			&TxOptions{Isolation: RepeatableRead, ReadOnly: true},
			sql.LevelRepeatableRead,
			true,
		},
		{
			"nil_defaults_to_serializable",
			nil,
			sql.LevelSerializable,
			false,
		},
		{
			"unknown_isolation_defaults_to_serializable",
			&TxOptions{Isolation: IsolationLevel(99)},
			sql.LevelSerializable,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlOpts := toSQLOptions(tt.opts)
			assert.Equal(t, tt.expectedLevel, sqlOpts.Isolation)
			assert.Equal(t, tt.expectedReadOnly, sqlOpts.ReadOnly)
		})
	}
}

func TestTx_BeginTx_returns_nested_error(t *testing.T) {
	tx := &Tx{tx: nil}
	_, err := tx.BeginTx(context.Background(), nil)
	assert.ErrorIs(t, err, ErrNestedTransaction)
}

func TestTx_Ping_returns_error(t *testing.T) {
	tx := &Tx{tx: nil}
	err := tx.Ping(context.Background())
	assert.ErrorIs(t, err, ErrPingNotSupported)
}

func TestTx_Close_is_noop(t *testing.T) {
	tx := &Tx{tx: nil}
	err := tx.Close()
	assert.NoError(t, err)
}

func TestSentinelErrors(t *testing.T) {
	assert.NotEqual(t, ErrNestedTransaction, ErrTransactionRolledBack)
	assert.NotEqual(t, ErrNestedTransaction, ErrPingNotSupported)
	assert.NotEqual(t, ErrTransactionRolledBack, ErrPingNotSupported)
}
