package config

import (
	"errors"
	"os"
	"testing"
)

func TestLoadAccountConfig_WithValidEnv(t *testing.T) {
	clearAccountEnv(t)
	t.Setenv("DEPOSIT_CLEARING_ACCOUNT_ID", "clearing-account-uuid")

	cfg, err := LoadAccountConfig()
	if err != nil {
		t.Fatalf("LoadAccountConfig() error = %v, want nil", err)
	}

	if cfg.DepositClearingAccountID != "clearing-account-uuid" {
		t.Errorf("DepositClearingAccountID = %s, want clearing-account-uuid", cfg.DepositClearingAccountID)
	}
}

func TestLoadAccountConfig_WithOptionalNostroAccount(t *testing.T) {
	clearAccountEnv(t)
	t.Setenv("DEPOSIT_CLEARING_ACCOUNT_ID", "clearing-account-uuid")
	t.Setenv("NOSTRO_ACCOUNT_ID", "nostro-account-uuid")

	cfg, err := LoadAccountConfig()
	if err != nil {
		t.Fatalf("LoadAccountConfig() error = %v, want nil", err)
	}

	if cfg.DepositClearingAccountID != "clearing-account-uuid" {
		t.Errorf("DepositClearingAccountID = %s, want clearing-account-uuid", cfg.DepositClearingAccountID)
	}
	if cfg.NostroAccountID != "nostro-account-uuid" {
		t.Errorf("NostroAccountID = %s, want nostro-account-uuid", cfg.NostroAccountID)
	}
}

func TestLoadAccountConfig_WithoutEnv(t *testing.T) {
	clearAccountEnv(t)

	_, err := LoadAccountConfig()
	if err == nil {
		t.Error("LoadAccountConfig() error = nil, want error for missing DEPOSIT_CLEARING_ACCOUNT_ID")
	}
	if !errors.Is(err, ErrEmptyDepositClearingAccountID) {
		t.Errorf("LoadAccountConfig() error = %v, want ErrEmptyDepositClearingAccountID", err)
	}
}

func TestLoadAccountConfig_WhitespaceOnlyEnv(t *testing.T) {
	clearAccountEnv(t)
	t.Setenv("DEPOSIT_CLEARING_ACCOUNT_ID", "   \t\n   ")

	_, err := LoadAccountConfig()
	if err == nil {
		t.Error("LoadAccountConfig() error = nil, want error for whitespace-only DEPOSIT_CLEARING_ACCOUNT_ID")
	}
}

func TestLoadAccountConfig_TrimsWhitespace(t *testing.T) {
	clearAccountEnv(t)
	t.Setenv("DEPOSIT_CLEARING_ACCOUNT_ID", "  clearing-account-uuid  ")
	t.Setenv("NOSTRO_ACCOUNT_ID", "  nostro-account-uuid  ")

	cfg, err := LoadAccountConfig()
	if err != nil {
		t.Fatalf("LoadAccountConfig() error = %v, want nil", err)
	}

	if cfg.DepositClearingAccountID != "clearing-account-uuid" {
		t.Errorf("DepositClearingAccountID = %q, want %q (should trim whitespace)", cfg.DepositClearingAccountID, "clearing-account-uuid")
	}
	if cfg.NostroAccountID != "nostro-account-uuid" {
		t.Errorf("NostroAccountID = %q, want %q (should trim whitespace)", cfg.NostroAccountID, "nostro-account-uuid")
	}
}

func TestAccountConfig_Validate_ValidConfig(t *testing.T) {
	cfg := &AccountConfig{
		DepositClearingAccountID: "clearing-account-uuid",
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestAccountConfig_Validate_EmptyDepositClearingAccountID(t *testing.T) {
	cfg := &AccountConfig{
		DepositClearingAccountID: "",
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for empty DepositClearingAccountID")
	}
	if !errors.Is(err, ErrEmptyDepositClearingAccountID) {
		t.Errorf("Validate() error = %v, want ErrEmptyDepositClearingAccountID", err)
	}
}

func TestAccountConfig_Validate_WithNostroAccount(t *testing.T) {
	cfg := &AccountConfig{
		DepositClearingAccountID: "clearing-account-uuid",
		NostroAccountID:          "nostro-account-uuid",
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestAccountConfig_Validate_EmptyNostroAccountIsAllowed(t *testing.T) {
	cfg := &AccountConfig{
		DepositClearingAccountID: "clearing-account-uuid",
		NostroAccountID:          "",
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil (empty nostro account should be allowed)", err)
	}
}

// clearAccountEnv clears environment variables used in tests
func clearAccountEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"DEPOSIT_CLEARING_ACCOUNT_ID",
		"NOSTRO_ACCOUNT_ID",
	}
	for _, key := range envVars {
		_ = os.Unsetenv(key)
	}
}
