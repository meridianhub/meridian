package stripe

import (
	"encoding/json"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"
)

func TestNewWebhookAdapter(t *testing.T) {
	t.Run("valid secret", func(t *testing.T) {
		a, err := NewWebhookAdapter("whsec_test123")
		require.NoError(t, err)
		assert.NotNil(t, a)
	})

	t.Run("empty secret", func(t *testing.T) {
		_, err := NewWebhookAdapter("")
		require.ErrorIs(t, err, ErrEmptyEndpointSecret)
	})
}

func TestParseWebhook_MissingSignature(t *testing.T) {
	a, err := NewWebhookAdapter("whsec_test")
	require.NoError(t, err)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/webhook", nil)
	// No Stripe-Signature header
	_, err = a.ParseWebhook(req, []byte("{}"))
	require.ErrorIs(t, err, ErrWebhookMissingSignature)
}

func TestParseWebhook_InvalidSignature(t *testing.T) {
	a, err := NewWebhookAdapter("whsec_test")
	require.NoError(t, err)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/webhook", nil)
	req.Header.Set("Stripe-Signature", "t=1234,v1=bad_signature")
	_, err = a.ParseWebhook(req, []byte(`{"id":"evt_1","type":"payment_intent.succeeded"}`))
	require.ErrorIs(t, err, ErrWebhookInvalidSignature)
}

// --- Direct tests of parse functions (bypass signature validation) ---

func TestParsePaymentIntentSucceeded(t *testing.T) {
	a := &WebhookAdapter{endpointSecret: "whsec_test"}

	t.Run("happy path", func(t *testing.T) {
		pi := stripego.PaymentIntent{
			ID:       "pi_success",
			Amount:   5000,
			Currency: "usd",
			Metadata: map[string]string{"payment_order_id": "po-123"},
		}
		raw, _ := json.Marshal(pi)
		event := &stripego.Event{
			ID:      "evt_1",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parsePaymentIntentSucceeded(event)
		require.NoError(t, err)
		assert.Equal(t, "evt_1", result.EventID)
		assert.Equal(t, "pi_success", result.GatewayReferenceID)
		assert.Equal(t, "po-123", result.PaymentOrderID)
		assert.Equal(t, "SETTLED", result.Status)
		assert.Equal(t, int64(5000), result.AmountMinorUnits)
		assert.Equal(t, "usd", result.Currency)
		assert.Equal(t, time.Unix(1700000000, 0), result.Timestamp)
	})

	t.Run("missing payment_order_id in metadata", func(t *testing.T) {
		pi := stripego.PaymentIntent{
			ID:       "pi_no_po",
			Amount:   1000,
			Currency: "gbp",
			Metadata: map[string]string{"other_key": "other_value"},
		}
		raw, _ := json.Marshal(pi)
		event := &stripego.Event{
			ID:      "evt_2",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parsePaymentIntentSucceeded(event)
		require.NoError(t, err)
		assert.Empty(t, result.PaymentOrderID, "missing payment_order_id should produce empty string, not error")
		assert.Equal(t, int64(1000), result.AmountMinorUnits)
	})

	t.Run("nil metadata", func(t *testing.T) {
		pi := stripego.PaymentIntent{
			ID:       "pi_nil_meta",
			Amount:   2000,
			Currency: "eur",
		}
		raw, _ := json.Marshal(pi)
		event := &stripego.Event{
			ID:      "evt_3",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parsePaymentIntentSucceeded(event)
		require.NoError(t, err)
		assert.Empty(t, result.PaymentOrderID)
	})

	t.Run("malformed JSON", func(t *testing.T) {
		event := &stripego.Event{
			ID:   "evt_bad",
			Data: &stripego.EventData{Raw: json.RawMessage(`{invalid}`)},
		}
		_, err := a.parsePaymentIntentSucceeded(event)
		require.Error(t, err)
	})
}

func TestParsePaymentIntentFailed(t *testing.T) {
	a := &WebhookAdapter{endpointSecret: "whsec_test"}

	t.Run("with payment error", func(t *testing.T) {
		pi := stripego.PaymentIntent{
			ID:       "pi_failed",
			Amount:   3000,
			Currency: "usd",
			Metadata: map[string]string{"payment_order_id": "po-456"},
			LastPaymentError: &stripego.Error{
				Msg: "Your card was declined.",
			},
		}
		raw, _ := json.Marshal(pi)
		event := &stripego.Event{
			ID:      "evt_4",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parsePaymentIntentFailed(event)
		require.NoError(t, err)
		assert.Equal(t, "REJECTED", result.Status)
		assert.Equal(t, "Your card was declined.", result.Message)
		assert.Equal(t, "po-456", result.PaymentOrderID)
		assert.Equal(t, int64(3000), result.AmountMinorUnits)
		assert.Equal(t, "usd", result.Currency)
	})

	t.Run("nil LastPaymentError", func(t *testing.T) {
		pi := stripego.PaymentIntent{
			ID:       "pi_failed_no_error",
			Metadata: map[string]string{"payment_order_id": "po-789"},
		}
		raw, _ := json.Marshal(pi)
		event := &stripego.Event{
			ID:      "evt_5",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parsePaymentIntentFailed(event)
		require.NoError(t, err)
		assert.Equal(t, "REJECTED", result.Status)
		assert.Empty(t, result.Message, "nil LastPaymentError should produce empty message")
	})
}

func TestParseChargeRefunded(t *testing.T) {
	a := &WebhookAdapter{endpointSecret: "whsec_test"}

	t.Run("with PaymentIntent metadata", func(t *testing.T) {
		charge := stripego.Charge{
			ID:             "ch_refund1",
			Amount:         4000,
			AmountRefunded: 4000, // full refund
			Currency:       "gbp",
			PaymentIntent: &stripego.PaymentIntent{
				Metadata: map[string]string{"payment_order_id": "po-from-pi"},
			},
			Metadata: map[string]string{"payment_order_id": "po-from-charge"},
		}
		raw, _ := json.Marshal(charge)
		event := &stripego.Event{
			ID:      "evt_6",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parseChargeRefunded(event)
		require.NoError(t, err)
		assert.Equal(t, "po-from-pi", result.PaymentOrderID, "should prefer PaymentIntent metadata")
		assert.Equal(t, "REFUNDED", result.Status)
		assert.Equal(t, int64(4000), result.AmountMinorUnits, "should use AmountRefunded, not Amount")
		assert.Equal(t, "gbp", result.Currency, "refund should carry currency")
	})

	t.Run("partial refund uses AmountRefunded not Amount", func(t *testing.T) {
		charge := stripego.Charge{
			ID:             "ch_partial",
			Amount:         5000, // original charge
			AmountRefunded: 2000, // partial refund
			Currency:       "eur",
			Metadata:       map[string]string{"payment_order_id": "po-partial"},
		}
		raw, _ := json.Marshal(charge)
		event := &stripego.Event{
			ID:      "evt_7",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parseChargeRefunded(event)
		require.NoError(t, err)
		assert.Equal(t, "po-partial", result.PaymentOrderID)
		assert.Equal(t, int64(2000), result.AmountMinorUnits, "partial refund should report refunded amount, not full charge")
		assert.Equal(t, "eur", result.Currency)
	})

	t.Run("no metadata anywhere", func(t *testing.T) {
		charge := stripego.Charge{
			ID:             "ch_refund_no_meta",
			Amount:         1000,
			AmountRefunded: 1000,
			Currency:       "usd",
		}
		raw, _ := json.Marshal(charge)
		event := &stripego.Event{
			ID:      "evt_8",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parseChargeRefunded(event)
		require.NoError(t, err)
		assert.Empty(t, result.PaymentOrderID)
		assert.Equal(t, int64(1000), result.AmountMinorUnits)
	})
}

func TestParseChargeDisputed(t *testing.T) {
	a := &WebhookAdapter{endpointSecret: "whsec_test"}

	t.Run("happy path", func(t *testing.T) {
		// Webhook delivers expanded charge inside dispute (not the SDK's Dispute struct)
		raw := json.RawMessage(`{
			"id": "dp_test",
			"reason": "fraudulent",
			"status": "needs_response",
			"charge": {
				"id": "ch_disputed",
				"metadata": {"payment_order_id": "po-disputed"}
			}
		}`)
		event := &stripego.Event{
			ID:      "evt_9",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parseChargeDisputed(event)
		require.NoError(t, err)
		assert.Equal(t, "evt_9", result.EventID)
		assert.Equal(t, "ch_disputed", result.GatewayReferenceID)
		assert.Equal(t, "po-disputed", result.PaymentOrderID)
		assert.Equal(t, "DISPUTED", result.Status)
		assert.Equal(t, "dispute reason: fraudulent", result.Message)
	})

	t.Run("charge with no metadata", func(t *testing.T) {
		raw := json.RawMessage(`{
			"id": "dp_no_meta",
			"reason": "general",
			"status": "needs_response",
			"charge": {
				"id": "ch_no_meta"
			}
		}`)
		event := &stripego.Event{
			ID:      "evt_10",
			Created: 1700000000,
			Data:    &stripego.EventData{Raw: raw},
		}

		result, err := a.parseChargeDisputed(event)
		require.NoError(t, err)
		assert.Empty(t, result.PaymentOrderID, "nil metadata should produce empty payment_order_id")
		assert.Equal(t, "ch_no_meta", result.GatewayReferenceID)
	})

	t.Run("malformed JSON", func(t *testing.T) {
		event := &stripego.Event{
			ID:   "evt_bad",
			Data: &stripego.EventData{Raw: json.RawMessage(`not json`)},
		}
		_, err := a.parseChargeDisputed(event)
		require.Error(t, err)
	})
}

func TestExtractPaymentOrderID(t *testing.T) {
	tests := []struct {
		name   string
		charge *stripego.Charge
		want   string
	}{
		{
			name: "from PaymentIntent metadata (preferred)",
			charge: &stripego.Charge{
				PaymentIntent: &stripego.PaymentIntent{
					Metadata: map[string]string{"payment_order_id": "po-pi"},
				},
				Metadata: map[string]string{"payment_order_id": "po-charge"},
			},
			want: "po-pi",
		},
		{
			name: "fallback to charge metadata when PaymentIntent nil",
			charge: &stripego.Charge{
				Metadata: map[string]string{"payment_order_id": "po-charge"},
			},
			want: "po-charge",
		},
		{
			name: "fallback to charge metadata when PI metadata nil",
			charge: &stripego.Charge{
				PaymentIntent: &stripego.PaymentIntent{},
				Metadata:      map[string]string{"payment_order_id": "po-charge"},
			},
			want: "po-charge",
		},
		{
			name: "fallback when PI metadata has no payment_order_id key",
			charge: &stripego.Charge{
				PaymentIntent: &stripego.PaymentIntent{
					Metadata: map[string]string{"other": "value"},
				},
				Metadata: map[string]string{"payment_order_id": "po-charge"},
			},
			want: "po-charge",
		},
		{
			name:   "nil metadata everywhere",
			charge: &stripego.Charge{},
			want:   "",
		},
		{
			name: "both metadata present but no payment_order_id anywhere",
			charge: &stripego.Charge{
				PaymentIntent: &stripego.PaymentIntent{
					Metadata: map[string]string{"other": "value"},
				},
				Metadata: map[string]string{"different": "data"},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPaymentOrderID(tt.charge)
			assert.Equal(t, tt.want, got)
		})
	}
}
