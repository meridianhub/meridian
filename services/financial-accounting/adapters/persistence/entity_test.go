package persistence

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// --- FinancialBookingLogEntity ---

func TestFinancialBookingLogEntity_TableName(t *testing.T) {
	entity := FinancialBookingLogEntity{}
	assert.Equal(t, "financial_booking_log", entity.TableName())
}

func TestFinancialBookingLogEntity_AuditID(t *testing.T) {
	id := uuid.New()
	entity := FinancialBookingLogEntity{ID: id}
	assert.Equal(t, id.String(), entity.AuditID())
}

func TestFinancialBookingLogEntity_AuditTableName(t *testing.T) {
	entity := FinancialBookingLogEntity{}
	assert.Equal(t, "financial_booking_log", entity.AuditTableName())
}

func TestFinancialBookingLogEntity_AuditID_ZeroUUID(t *testing.T) {
	entity := FinancialBookingLogEntity{}
	assert.Equal(t, uuid.Nil.String(), entity.AuditID())
}

// --- LedgerPostingEntity ---

func TestLedgerPostingEntity_TableName(t *testing.T) {
	entity := LedgerPostingEntity{}
	assert.Equal(t, "ledger_posting", entity.TableName())
}

func TestLedgerPostingEntity_AuditID(t *testing.T) {
	id := uuid.New()
	entity := LedgerPostingEntity{ID: id}
	assert.Equal(t, id.String(), entity.AuditID())
}

func TestLedgerPostingEntity_AuditTableName(t *testing.T) {
	entity := LedgerPostingEntity{}
	assert.Equal(t, "ledger_posting", entity.AuditTableName())
}

func TestLedgerPostingEntity_AuditID_ZeroUUID(t *testing.T) {
	entity := LedgerPostingEntity{}
	assert.Equal(t, uuid.Nil.String(), entity.AuditID())
}
