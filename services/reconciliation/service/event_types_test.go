package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDisputeCreatedEvent_Getters(t *testing.T) {
	e := DisputeCreatedEvent{
		DisputeID:  "disp-001",
		VarianceID: "var-001",
		RunID:      "run-001",
		AccountID:  "ACC-001",
		Reason:     "incorrect amount",
		RaisedBy:   "user-001",
	}

	assert.Equal(t, "disp-001", e.GetDisputeID())
	assert.Equal(t, "var-001", e.GetVarianceID())
	assert.Equal(t, "run-001", e.GetRunID())
	assert.Equal(t, "ACC-001", e.GetAccountID())
	assert.Equal(t, "incorrect amount", e.GetReason())
	assert.Equal(t, "user-001", e.GetRaisedBy())
}

func TestDisputeResolvedEvent_Getters(t *testing.T) {
	e := DisputeResolvedEvent{
		DisputeID:  "disp-001",
		VarianceID: "var-001",
		RunID:      "run-001",
		AccountID:  "ACC-001",
		Action:     "RESOLVED",
		Resolution: "accepted variance",
		ResolvedBy: "user-002",
	}

	assert.Equal(t, "disp-001", e.GetDisputeID())
	assert.Equal(t, "var-001", e.GetVarianceID())
	assert.Equal(t, "run-001", e.GetRunID())
	assert.Equal(t, "ACC-001", e.GetAccountID())
	assert.Equal(t, "RESOLVED", e.GetAction())
	assert.Equal(t, "accepted variance", e.GetResolution())
	assert.Equal(t, "user-002", e.GetResolvedBy())
}

func TestPositionLockRequestedEvent_Getters(t *testing.T) {
	e := PositionLockRequestedEvent{
		RunID:       "run-001",
		AccountID:   "ACC-001",
		Scope:       "ACCOUNT",
		PeriodStart: "2026-01-01T00:00:00Z",
		PeriodEnd:   "2026-01-02T00:00:00Z",
		Status:      "RUNNING",
	}

	assert.Equal(t, "run-001", e.GetRunID())
	assert.Equal(t, "ACC-001", e.GetAccountID())
	assert.Equal(t, "ACCOUNT", e.GetScope())
	assert.Equal(t, "2026-01-01T00:00:00Z", e.GetPeriodStart())
	assert.Equal(t, "2026-01-02T00:00:00Z", e.GetPeriodEnd())
	assert.Equal(t, "RUNNING", e.GetStatus())
}
