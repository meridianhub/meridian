package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRun_EscalateDunning(t *testing.T) {
	t.Run("escalates from level 0 to 1", func(t *testing.T) {
		run := &BillingRun{
			Status:       BillingRunStatusFailed,
			DunningLevel: 0,
		}

		err := run.EscalateDunning()
		require.NoError(t, err)
		assert.Equal(t, 1, run.DunningLevel)
		assert.NotNil(t, run.LastRetryAt)
	})

	t.Run("escalates through all levels", func(t *testing.T) {
		run := &BillingRun{
			Status:       BillingRunStatusFailed,
			DunningLevel: 0,
		}

		for expectedLevel := 1; expectedLevel <= MaxDunningLevel; expectedLevel++ {
			err := run.EscalateDunning()
			require.NoError(t, err)
			assert.Equal(t, expectedLevel, run.DunningLevel)
		}
	})

	t.Run("rejects escalation on non-failed run", func(t *testing.T) {
		run := &BillingRun{
			Status:       BillingRunStatusProcessing,
			DunningLevel: 0,
		}

		err := run.EscalateDunning()
		assert.ErrorIs(t, err, ErrInvalidBillingRunTransition)
	})

	t.Run("rejects escalation beyond max level", func(t *testing.T) {
		run := &BillingRun{
			Status:       BillingRunStatusFailed,
			DunningLevel: MaxDunningLevel + 1,
		}

		err := run.EscalateDunning()
		assert.ErrorIs(t, err, ErrBillingRunTerminal)
	})
}

func TestBillingRun_NeedsFreezing(t *testing.T) {
	t.Run("returns false below max level", func(t *testing.T) {
		run := &BillingRun{DunningLevel: 2}
		assert.False(t, run.NeedsFreezing())
	})

	t.Run("returns true at max level", func(t *testing.T) {
		run := &BillingRun{DunningLevel: MaxDunningLevel}
		assert.True(t, run.NeedsFreezing())
	})

	t.Run("returns true above max level", func(t *testing.T) {
		run := &BillingRun{DunningLevel: MaxDunningLevel + 1}
		assert.True(t, run.NeedsFreezing())
	})
}

func TestBillingRun_DunningRetryDelay(t *testing.T) {
	t.Run("level 0 returns 24h", func(t *testing.T) {
		run := &BillingRun{DunningLevel: 0}
		assert.Equal(t, 24*time.Hour, run.DunningRetryDelay())
	})

	t.Run("level 1 returns 72h", func(t *testing.T) {
		run := &BillingRun{DunningLevel: 1}
		assert.Equal(t, 72*time.Hour, run.DunningRetryDelay())
	})

	t.Run("level 2 returns 168h", func(t *testing.T) {
		run := &BillingRun{DunningLevel: 2}
		assert.Equal(t, 168*time.Hour, run.DunningRetryDelay())
	})

	t.Run("level 3 returns zero duration", func(t *testing.T) {
		run := &BillingRun{DunningLevel: 3}
		assert.Equal(t, time.Duration(0), run.DunningRetryDelay())
	})
}
