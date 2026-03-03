package registry_test

import (
	"sync"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/event-router/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSagaRegistry_Reload_RegistersEventTriggeredSagas(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	sagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "process_settlement",
			Trigger: "event:position-keeping.transaction-captured.v1",
			Script:  "def run(ctx, input): pass",
		},
	}

	err = reg.Reload(sagas)
	require.NoError(t, err)

	results := reg.GetApplicableSagas("position-keeping.transaction-captured.v1")
	require.Len(t, results, 1)
	assert.Equal(t, "process_settlement", results[0].Definition.GetName())
}

func TestSagaRegistry_Reload_IgnoresNonEventTriggers(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	sagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "api_saga",
			Trigger: "api:/v1/settlements",
			Script:  "def run(ctx, input): pass",
		},
		{
			Name:    "webhook_saga",
			Trigger: "webhook:stripe_payment",
			Script:  "def run(ctx, input): pass",
		},
		{
			Name:    "scheduled_saga",
			Trigger: "scheduled:daily_reconciliation",
			Script:  "def run(ctx, input): pass",
		},
	}

	err = reg.Reload(sagas)
	require.NoError(t, err)

	// All non-event triggers should be ignored
	results := reg.GetApplicableSagas("position-keeping.transaction-captured.v1")
	assert.Nil(t, results)
}

func TestSagaRegistry_GetApplicableSagas_MultipleSagasForSameChannel(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	channel := "position-keeping.transaction-captured.v1"
	sagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "settlement_a",
			Trigger: "event:" + channel,
			Script:  "def run(ctx, input): pass",
		},
		{
			Name:    "settlement_b",
			Trigger: "event:" + channel,
			Script:  "def run(ctx, input): pass",
		},
	}

	err = reg.Reload(sagas)
	require.NoError(t, err)

	results := reg.GetApplicableSagas(channel)
	require.Len(t, results, 2)

	names := []string{results[0].Definition.GetName(), results[1].Definition.GetName()}
	assert.ElementsMatch(t, []string{"settlement_a", "settlement_b"}, names)
}

func TestSagaRegistry_GetApplicableSagas_UnknownChannelReturnsNil(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	err = reg.Reload([]*controlplanev1.SagaDefinition{
		{
			Name:    "process_settlement",
			Trigger: "event:position-keeping.transaction-captured.v1",
			Script:  "def run(ctx, input): pass",
		},
	})
	require.NoError(t, err)

	results := reg.GetApplicableSagas("unknown-channel")
	assert.Nil(t, results)
}

func TestSagaRegistry_Reload_ClearsPreviousRegistrations(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	// First load with one saga
	err = reg.Reload([]*controlplanev1.SagaDefinition{
		{
			Name:    "old_saga",
			Trigger: "event:some-channel",
			Script:  "def run(ctx, input): pass",
		},
	})
	require.NoError(t, err)

	results := reg.GetApplicableSagas("some-channel")
	require.Len(t, results, 1)

	// Reload with different saga
	err = reg.Reload([]*controlplanev1.SagaDefinition{
		{
			Name:    "new_saga",
			Trigger: "event:other-channel",
			Script:  "def run(ctx, input): pass",
		},
	})
	require.NoError(t, err)

	// Old channel should no longer be registered
	oldResults := reg.GetApplicableSagas("some-channel")
	assert.Nil(t, oldResults)

	// New channel should be registered
	newResults := reg.GetApplicableSagas("other-channel")
	require.Len(t, newResults, 1)
	assert.Equal(t, "new_saga", newResults[0].Definition.GetName())
}

func TestSagaRegistry_Reload_WithValidCELFilter(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	filter := `event.amount > 0`
	sagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "filtered_saga",
			Trigger: "event:position-keeping.transaction-captured.v1",
			Script:  "def run(ctx, input): pass",
			Filter:  &filter,
		},
	}

	err = reg.Reload(sagas)
	require.NoError(t, err)

	results := reg.GetApplicableSagas("position-keeping.transaction-captured.v1")
	require.Len(t, results, 1)
	assert.Equal(t, "filtered_saga", results[0].Definition.GetName())
	assert.NotNil(t, results[0].FilterProgram, "FilterProgram should be compiled and non-nil")
}

func TestSagaRegistry_Reload_WithInvalidCELFilter_ReturnsError(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	filter := `this is not valid CEL !!!`
	sagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "bad_filter_saga",
			Trigger: "event:some-channel",
			Script:  "def run(ctx, input): pass",
			Filter:  &filter,
		},
	}

	err = reg.Reload(sagas)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad_filter_saga")
}

func TestSagaRegistry_Reload_WithNoFilter_FilterProgramIsNil(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	sagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "unfiltered_saga",
			Trigger: "event:some-channel",
			Script:  "def run(ctx, input): pass",
			// No Filter field
		},
	}

	err = reg.Reload(sagas)
	require.NoError(t, err)

	results := reg.GetApplicableSagas("some-channel")
	require.Len(t, results, 1)
	assert.Nil(t, results[0].FilterProgram, "FilterProgram should be nil when no filter is set")
}

func TestSagaRegistry_ThreadSafety_ConcurrentReadsAndReload(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	// Seed with initial data
	err = reg.Reload([]*controlplanev1.SagaDefinition{
		{
			Name:    "initial_saga",
			Trigger: "event:test-channel",
			Script:  "def run(ctx, input): pass",
		},
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	// Concurrent readers
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				results := reg.GetApplicableSagas("test-channel")
				_ = results // Just ensure no panic or race
			}
		}()
	}

	// Concurrent writer (Reload)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 10; j++ {
			if err := reg.Reload([]*controlplanev1.SagaDefinition{
				{
					Name:    "updated_saga",
					Trigger: "event:test-channel",
					Script:  "def run(ctx, input): pass",
				},
			}); err != nil {
				errCh <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent reload error: %v", err)
	}
}

func TestSagaRegistry_Reload_EmptySlice_ClearsAll(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	err = reg.Reload([]*controlplanev1.SagaDefinition{
		{
			Name:    "some_saga",
			Trigger: "event:some-channel",
			Script:  "def run(ctx, input): pass",
		},
	})
	require.NoError(t, err)

	// Now reload with empty slice
	err = reg.Reload([]*controlplanev1.SagaDefinition{})
	require.NoError(t, err)

	results := reg.GetApplicableSagas("some-channel")
	assert.Nil(t, results)
}

func TestSagaRegistry_Reload_MultipleDifferentChannels(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	sagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "saga_for_channel_a",
			Trigger: "event:channel-a",
			Script:  "def run(ctx, input): pass",
		},
		{
			Name:    "saga_for_channel_b",
			Trigger: "event:channel-b",
			Script:  "def run(ctx, input): pass",
		},
		{
			Name:    "another_saga_for_channel_a",
			Trigger: "event:channel-a",
			Script:  "def run(ctx, input): pass",
		},
	}

	err = reg.Reload(sagas)
	require.NoError(t, err)

	channelA := reg.GetApplicableSagas("channel-a")
	require.Len(t, channelA, 2)

	channelB := reg.GetApplicableSagas("channel-b")
	require.Len(t, channelB, 1)
	assert.Equal(t, "saga_for_channel_b", channelB[0].Definition.GetName())
}
