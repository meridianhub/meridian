package idempotency

import (
	"errors"
	"testing"
	"time"
)

func TestStateMachine_ValidateTransition(t *testing.T) {
	sm := NewStateMachine(nil)

	tests := []struct {
		name    string
		from    State
		to      State
		wantErr bool
	}{
		// Valid transitions
		{
			name:    "NONE to PENDING is valid",
			from:    StateNone,
			to:      StatePending,
			wantErr: false,
		},
		{
			name:    "PENDING to COMPLETED is valid",
			from:    StatePending,
			to:      StateCompleted,
			wantErr: false,
		},
		{
			name:    "PENDING to FAILED is valid",
			from:    StatePending,
			to:      StateFailed,
			wantErr: false,
		},

		// Invalid transitions from PENDING
		{
			name:    "PENDING to PENDING is rejected",
			from:    StatePending,
			to:      StatePending,
			wantErr: true,
		},

		// Invalid transitions from COMPLETED (terminal state)
		{
			name:    "COMPLETED to PENDING is rejected",
			from:    StateCompleted,
			to:      StatePending,
			wantErr: true,
		},
		{
			name:    "COMPLETED to COMPLETED is rejected",
			from:    StateCompleted,
			to:      StateCompleted,
			wantErr: true,
		},
		{
			name:    "COMPLETED to FAILED is rejected",
			from:    StateCompleted,
			to:      StateFailed,
			wantErr: true,
		},
		{
			name:    "COMPLETED to NONE is rejected",
			from:    StateCompleted,
			to:      StateNone,
			wantErr: true,
		},

		// Invalid transitions from FAILED (terminal state)
		{
			name:    "FAILED to PENDING is rejected",
			from:    StateFailed,
			to:      StatePending,
			wantErr: true,
		},
		{
			name:    "FAILED to COMPLETED is rejected",
			from:    StateFailed,
			to:      StateCompleted,
			wantErr: true,
		},
		{
			name:    "FAILED to FAILED is rejected",
			from:    StateFailed,
			to:      StateFailed,
			wantErr: true,
		},
		{
			name:    "FAILED to NONE is rejected",
			from:    StateFailed,
			to:      StateNone,
			wantErr: true,
		},

		// Invalid transitions from NONE (except to PENDING)
		{
			name:    "NONE to COMPLETED is rejected",
			from:    StateNone,
			to:      StateCompleted,
			wantErr: true,
		},
		{
			name:    "NONE to FAILED is rejected",
			from:    StateNone,
			to:      StateFailed,
			wantErr: true,
		},
		{
			name:    "NONE to NONE is rejected",
			from:    StateNone,
			to:      StateNone,
			wantErr: true,
		},

		// Edge case: PENDING to NONE is invalid
		{
			name:    "PENDING to NONE is rejected",
			from:    StatePending,
			to:      StateNone,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sm.ValidateTransition(tt.from, tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTransition(%s, %s) error = %v, wantErr %v",
					tt.from, tt.to, err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("ValidateTransition(%s, %s) expected ErrInvalidTransition, got %v",
					tt.from, tt.to, err)
			}
		})
	}
}

func TestStateMachine_IsTerminal(t *testing.T) {
	sm := NewStateMachine(nil)

	tests := []struct {
		state    State
		terminal bool
	}{
		{StateNone, false},
		{StatePending, false},
		{StateCompleted, true},
		{StateFailed, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := sm.IsTerminal(tt.state); got != tt.terminal {
				t.Errorf("IsTerminal(%s) = %v, want %v", tt.state, got, tt.terminal)
			}
		})
	}
}

func TestStateMachine_IsPending(t *testing.T) {
	sm := NewStateMachine(nil)

	tests := []struct {
		state   State
		pending bool
	}{
		{StateNone, false},
		{StatePending, true},
		{StateCompleted, false},
		{StateFailed, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := sm.IsPending(tt.state); got != tt.pending {
				t.Errorf("IsPending(%s) = %v, want %v", tt.state, got, tt.pending)
			}
		})
	}
}

func TestStateMachine_IsStale(t *testing.T) {
	config := Config{StaleKeyTimeout: 15 * time.Minute}
	sm := NewStateMachine(&config)

	now := time.Now()

	tests := []struct {
		name         string
		pendingSince time.Time
		now          time.Time
		wantStale    bool
	}{
		{
			name:         "recent key is not stale",
			pendingSince: now.Add(-5 * time.Minute),
			now:          now,
			wantStale:    false,
		},
		{
			name:         "key at exact timeout boundary is not stale",
			pendingSince: now.Add(-15 * time.Minute),
			now:          now,
			wantStale:    false,
		},
		{
			name:         "key past timeout is stale",
			pendingSince: now.Add(-16 * time.Minute),
			now:          now,
			wantStale:    true,
		},
		{
			name:         "key way past timeout is stale",
			pendingSince: now.Add(-1 * time.Hour),
			now:          now,
			wantStale:    true,
		},
		{
			name:         "zero time is not stale",
			pendingSince: time.Time{},
			now:          now,
			wantStale:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sm.IsStale(tt.pendingSince, tt.now); got != tt.wantStale {
				t.Errorf("IsStale() = %v, want %v", got, tt.wantStale)
			}
		})
	}
}

func TestStateMachine_StaleKeyTimeout(t *testing.T) {
	// Test with custom config
	customTimeout := 30 * time.Minute
	config := Config{StaleKeyTimeout: customTimeout}
	sm := NewStateMachine(&config)

	if got := sm.StaleKeyTimeout(); got != customTimeout {
		t.Errorf("StaleKeyTimeout() = %v, want %v", got, customTimeout)
	}

	// Test with default config
	smDefault := NewStateMachine(nil)
	expectedDefault := 15 * time.Minute
	if got := smDefault.StaleKeyTimeout(); got != expectedDefault {
		t.Errorf("StaleKeyTimeout() with default config = %v, want %v", got, expectedDefault)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.StaleKeyTimeout != 15*time.Minute {
		t.Errorf("DefaultConfig().StaleKeyTimeout = %v, want %v",
			config.StaleKeyTimeout, 15*time.Minute)
	}
}

func TestStatusToState(t *testing.T) {
	tests := []struct {
		status OperationStatus
		state  State
	}{
		{StatusPending, StatePending},
		{StatusCompleted, StateCompleted},
		{StatusFailed, StateFailed},
		{"", StateNone},
		{"unknown", StateNone},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := StatusToState(tt.status); got != tt.state {
				t.Errorf("StatusToState(%s) = %v, want %v", tt.status, got, tt.state)
			}
		})
	}
}

func TestStateToStatus(t *testing.T) {
	tests := []struct {
		state  State
		status OperationStatus
	}{
		{StatePending, StatusPending},
		{StateCompleted, StatusCompleted},
		{StateFailed, StatusFailed},
		{StateNone, ""},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := StateToStatus(tt.state); got != tt.status {
				t.Errorf("StateToStatus(%s) = %v, want %v", tt.state, got, tt.status)
			}
		})
	}
}

func TestStateMachine_AttemptTransition(t *testing.T) {
	sm := NewStateMachine(nil)

	t.Run("successful transition NONE to PENDING", func(t *testing.T) {
		result := sm.AttemptTransition(StateNone, StatePending)
		if !result.Allowed {
			t.Error("Expected transition to be allowed")
		}
		if result.PreviousState != StateNone {
			t.Errorf("PreviousState = %v, want %v", result.PreviousState, StateNone)
		}
		if result.NewState != StatePending {
			t.Errorf("NewState = %v, want %v", result.NewState, StatePending)
		}
		if result.Error != nil {
			t.Errorf("Error = %v, want nil", result.Error)
		}
	})

	t.Run("successful transition PENDING to COMPLETED", func(t *testing.T) {
		result := sm.AttemptTransition(StatePending, StateCompleted)
		if !result.Allowed {
			t.Error("Expected transition to be allowed")
		}
		if result.NewState != StateCompleted {
			t.Errorf("NewState = %v, want %v", result.NewState, StateCompleted)
		}
	})

	t.Run("failed transition PENDING to PENDING", func(t *testing.T) {
		result := sm.AttemptTransition(StatePending, StatePending)
		if result.Allowed {
			t.Error("Expected transition to be rejected")
		}
		if result.PreviousState != StatePending {
			t.Errorf("PreviousState = %v, want %v", result.PreviousState, StatePending)
		}
		if result.NewState != StatePending {
			t.Errorf("NewState should remain %v on failure, got %v", StatePending, result.NewState)
		}
		if !errors.Is(result.Error, ErrInvalidTransition) {
			t.Errorf("Error = %v, want ErrInvalidTransition", result.Error)
		}
	})

	t.Run("failed transition COMPLETED to PENDING", func(t *testing.T) {
		result := sm.AttemptTransition(StateCompleted, StatePending)
		if result.Allowed {
			t.Error("Expected transition to be rejected")
		}
		if result.NewState != StateCompleted {
			t.Errorf("NewState should remain %v on failure, got %v", StateCompleted, result.NewState)
		}
		if !errors.Is(result.Error, ErrInvalidTransition) {
			t.Errorf("Error = %v, want ErrInvalidTransition", result.Error)
		}
	})
}

func TestStateMachine_UnknownState(t *testing.T) {
	sm := NewStateMachine(nil)

	// Test transitions from unknown state
	unknownState := State("unknown")
	err := sm.ValidateTransition(unknownState, StatePending)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Expected ErrInvalidTransition for unknown state, got %v", err)
	}
}

func TestState_Constants(t *testing.T) {
	// Verify state constants have expected values
	states := map[State]string{
		StateNone:      "none",
		StatePending:   "pending",
		StateCompleted: "completed",
		StateFailed:    "failed",
	}

	for state, expected := range states {
		if string(state) != expected {
			t.Errorf("State %v = %q, want %q", state, string(state), expected)
		}
	}
}

func TestError_Types(t *testing.T) {
	// Verify error types are distinct
	if errors.Is(ErrInvalidTransition, ErrStaleKey) {
		t.Error("ErrInvalidTransition and ErrStaleKey should be distinct")
	}

	// Verify error messages are meaningful
	if ErrInvalidTransition.Error() == "" {
		t.Error("ErrInvalidTransition should have non-empty message")
	}
	if ErrStaleKey.Error() == "" {
		t.Error("ErrStaleKey should have non-empty message")
	}
}

// TestStateMachine_TransitionMatrix verifies the complete state transition matrix
func TestStateMachine_TransitionMatrix(t *testing.T) {
	sm := NewStateMachine(nil)
	allStates := []State{StateNone, StatePending, StateCompleted, StateFailed}

	// Expected valid transitions (from -> to)
	validTransitions := map[State][]State{
		StateNone:      {StatePending},
		StatePending:   {StateCompleted, StateFailed},
		StateCompleted: {}, // Terminal - no valid transitions
		StateFailed:    {}, // Terminal - no valid transitions
	}

	for _, from := range allStates {
		for _, to := range allStates {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				err := sm.ValidateTransition(from, to)
				shouldBeValid := false
				for _, validTo := range validTransitions[from] {
					if to == validTo {
						shouldBeValid = true
						break
					}
				}

				if shouldBeValid && err != nil {
					t.Errorf("Transition %s -> %s should be valid, got error: %v",
						from, to, err)
				}
				if !shouldBeValid && err == nil {
					t.Errorf("Transition %s -> %s should be invalid, got no error",
						from, to)
				}
			})
		}
	}
}
