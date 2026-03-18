package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestNewMoneyFromInt(t *testing.T) {
	instrument := MustCurrencyToInstrument(CurrencyGBP)

	t.Run("creates money from positive int", func(t *testing.T) {
		m := NewMoneyFromInt(100, instrument)
		if m.Amount.IntPart() != 100 {
			t.Errorf("expected amount 100, got %v", m.Amount)
		}
		if m.Instrument.Code != "GBP" {
			t.Errorf("expected GBP, got %v", m.Instrument.Code)
		}
	})

	t.Run("creates money from zero", func(t *testing.T) {
		m := NewMoneyFromInt(0, instrument)
		if !m.Amount.IsZero() {
			t.Errorf("expected zero amount, got %v", m.Amount)
		}
	})

	t.Run("creates money from negative int", func(t *testing.T) {
		m := NewMoneyFromInt(-50, instrument)
		if m.Amount.IntPart() != -50 {
			t.Errorf("expected amount -50, got %v", m.Amount)
		}
	})
}

func TestNewAsset(t *testing.T) {
	instrument, err := NewInstrument("KWH", 1, "ENERGY", 3)
	if err != nil {
		t.Fatalf("failed to create instrument: %v", err)
	}

	t.Run("creates asset with decimal amount", func(t *testing.T) {
		amount := decimal.NewFromFloat(123.456)
		a := NewAsset(amount, instrument)
		if !a.Amount.Equal(amount) {
			t.Errorf("expected amount %v, got %v", amount, a.Amount)
		}
		if a.Instrument.Code != "KWH" {
			t.Errorf("expected KWH, got %v", a.Instrument.Code)
		}
	})

	t.Run("creates asset with zero amount", func(t *testing.T) {
		a := NewAsset(decimal.Zero, instrument)
		if !a.Amount.IsZero() {
			t.Errorf("expected zero amount, got %v", a.Amount)
		}
	})
}

func TestNewAssetFromString(t *testing.T) {
	instrument, err := NewInstrument("KWH", 1, "ENERGY", 3)
	if err != nil {
		t.Fatalf("failed to create instrument: %v", err)
	}

	t.Run("parses valid decimal string", func(t *testing.T) {
		a, err := NewAssetFromString("123.456", instrument)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := decimal.NewFromFloat(123.456)
		if !a.Amount.Equal(expected) {
			t.Errorf("expected amount %v, got %v", expected, a.Amount)
		}
	})

	t.Run("returns error for invalid string", func(t *testing.T) {
		_, err := NewAssetFromString("not-a-number", instrument)
		if err == nil {
			t.Error("expected error for invalid decimal string, got nil")
		}
	})

	t.Run("parses zero string", func(t *testing.T) {
		a, err := NewAssetFromString("0", instrument)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !a.Amount.IsZero() {
			t.Errorf("expected zero amount, got %v", a.Amount)
		}
	})
}

func TestNewAssetFromInt(t *testing.T) {
	instrument, err := NewInstrument("KWH", 1, "ENERGY", 3)
	if err != nil {
		t.Fatalf("failed to create instrument: %v", err)
	}

	t.Run("creates asset from positive int", func(t *testing.T) {
		a := NewAssetFromInt(500, instrument)
		if a.Amount.IntPart() != 500 {
			t.Errorf("expected amount 500, got %v", a.Amount)
		}
		if a.Instrument.Code != "KWH" {
			t.Errorf("expected KWH, got %v", a.Instrument.Code)
		}
	})

	t.Run("creates asset from zero", func(t *testing.T) {
		a := NewAssetFromInt(0, instrument)
		if !a.Amount.IsZero() {
			t.Errorf("expected zero amount, got %v", a.Amount)
		}
	})
}

func TestZeroAsset(t *testing.T) {
	instrument, err := NewInstrument("KWH", 1, "ENERGY", 3)
	if err != nil {
		t.Fatalf("failed to create instrument: %v", err)
	}

	t.Run("creates zero asset", func(t *testing.T) {
		a := ZeroAsset(instrument)
		if !a.Amount.IsZero() {
			t.Errorf("expected zero amount, got %v", a.Amount)
		}
		if a.Instrument.Code != "KWH" {
			t.Errorf("expected KWH instrument, got %v", a.Instrument.Code)
		}
	})
}

func TestNewQuantityValidated(t *testing.T) {
	t.Run("valid monetary quantity passes validation", func(t *testing.T) {
		gbpInstrument := MustCurrencyToInstrument(CurrencyGBP)
		amount := decimal.NewFromInt(100)
		q, err := NewQuantityValidated[Monetary](amount, gbpInstrument)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !q.Amount.Equal(amount) {
			t.Errorf("expected amount %v, got %v", amount, q.Amount)
		}
	})

	t.Run("commodity instrument fails monetary dimension validation", func(t *testing.T) {
		kwhInstrument, err := NewInstrument("KWH", 1, "ENERGY", 3)
		if err != nil {
			t.Fatalf("failed to create instrument: %v", err)
		}
		_, err = NewQuantityValidated[Monetary](decimal.NewFromInt(100), kwhInstrument)
		if err == nil {
			t.Error("expected error for dimension mismatch, got nil")
		}
	})
}
