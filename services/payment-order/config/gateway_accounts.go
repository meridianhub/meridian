// Package config provides configuration for the payment-order service.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// GatewayAccountMapping defines the mapping between a payment gateway and its contra-account.
// The contra-account is used for ledger postings when processing payments through this gateway.
type GatewayAccountMapping struct {
	// GatewayID is the payment gateway identifier (e.g., "stripe", "adyen", "mock").
	GatewayID string `json:"gateway_id"`
	// ContraAccountID is the nostro/acquirer account ID for this gateway.
	ContraAccountID string `json:"contra_account_id"`
	// AccountType indicates the type of contra-account ("NOSTRO" or "ACQUIRER").
	AccountType string `json:"account_type"`
}

// GatewayAccountConfig holds the configuration for all gateway-to-account mappings.
type GatewayAccountConfig struct {
	// Mappings contains the gateway-to-account mappings keyed by GatewayID.
	Mappings map[string]*GatewayAccountMapping
}

// Configuration errors
var (
	// ErrNoGatewayMapping is returned when no mapping exists for the specified gateway.
	ErrNoGatewayMapping = errors.New("no contra-account mapping for gateway")
	// ErrEmptyConfig is returned when the configuration has no mappings.
	ErrEmptyConfig = errors.New("gateway account configuration is empty")
	// ErrInvalidAccountType is returned when the account type is not valid.
	ErrInvalidAccountType = errors.New("invalid account type: must be NOSTRO or ACQUIRER")
	// ErrEmptyGatewayID is returned when a gateway ID is empty.
	ErrEmptyGatewayID = errors.New("gateway ID must not be empty")
	// ErrEmptyContraAccountID is returned when a contra-account ID is empty.
	ErrEmptyContraAccountID = errors.New("contra-account ID must not be empty")
	// ErrGatewayIDMismatch is returned when the map key doesn't match the mapping's gateway ID.
	ErrGatewayIDMismatch = errors.New("gateway ID mismatch between key and mapping")
)

// Valid account types
const (
	AccountTypeNostro   = "NOSTRO"
	AccountTypeAcquirer = "ACQUIRER"
)

// GetContraAccount returns the contra-account ID for the specified gateway.
// Returns ErrNoGatewayMapping if no mapping exists for the gateway.
func (c *GatewayAccountConfig) GetContraAccount(gatewayID string) (string, error) {
	if c.Mappings == nil {
		return "", fmt.Errorf("%w: %s", ErrNoGatewayMapping, gatewayID)
	}

	mapping, exists := c.Mappings[gatewayID]
	if !exists {
		return "", fmt.Errorf("%w: %s", ErrNoGatewayMapping, gatewayID)
	}

	return mapping.ContraAccountID, nil
}

// GetMapping returns the full mapping for the specified gateway.
// Returns ErrNoGatewayMapping if no mapping exists for the gateway.
func (c *GatewayAccountConfig) GetMapping(gatewayID string) (*GatewayAccountMapping, error) {
	if c.Mappings == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoGatewayMapping, gatewayID)
	}

	mapping, exists := c.Mappings[gatewayID]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrNoGatewayMapping, gatewayID)
	}

	return mapping, nil
}

// Validate validates the configuration.
func (c *GatewayAccountConfig) Validate() error {
	if len(c.Mappings) == 0 {
		return ErrEmptyConfig
	}

	for gatewayID, mapping := range c.Mappings {
		if gatewayID == "" {
			return ErrEmptyGatewayID
		}
		if mapping.GatewayID == "" {
			return fmt.Errorf("%w: mapping key %s", ErrEmptyGatewayID, gatewayID)
		}
		if mapping.ContraAccountID == "" {
			return fmt.Errorf("%w: gateway %s", ErrEmptyContraAccountID, gatewayID)
		}
		if mapping.AccountType != AccountTypeNostro && mapping.AccountType != AccountTypeAcquirer {
			return fmt.Errorf("%w: gateway %s has type %s", ErrInvalidAccountType, gatewayID, mapping.AccountType)
		}
		// Verify the map key matches the mapping's GatewayID
		if gatewayID != mapping.GatewayID {
			return fmt.Errorf("%w: key %s does not match mapping gateway_id %s", ErrGatewayIDMismatch, gatewayID, mapping.GatewayID)
		}
	}

	return nil
}

// LoadGatewayAccountConfig loads the gateway account configuration from environment or file.
//
// The configuration can be loaded in two ways (in order of precedence):
//  1. JSON file: Set GATEWAY_ACCOUNT_MAPPING_FILE to the path of a JSON config file
//  2. Environment variables: Set GATEWAY_{ID}_ACCOUNT_ID and GATEWAY_{ID}_ACCOUNT_TYPE
//     for each gateway (e.g., GATEWAY_STRIPE_ACCOUNT_ID, GATEWAY_STRIPE_ACCOUNT_TYPE)
//
// Environment variable format for individual gateways:
//   - GATEWAY_{ID}_ACCOUNT_ID: The contra-account UUID
//   - GATEWAY_{ID}_ACCOUNT_TYPE: Either "NOSTRO" or "ACQUIRER"
//
// JSON file format:
//
//	{
//	  "stripe": {"gateway_id": "stripe", "contra_account_id": "uuid-1", "account_type": "NOSTRO"},
//	  "mock": {"gateway_id": "mock", "contra_account_id": "uuid-2", "account_type": "ACQUIRER"}
//	}
func LoadGatewayAccountConfig() (*GatewayAccountConfig, error) {
	// First, try loading from JSON file
	configFile := os.Getenv("GATEWAY_ACCOUNT_MAPPING_FILE")
	if configFile != "" {
		return loadFromFile(configFile)
	}

	// Fall back to environment variables
	return loadFromEnv()
}

// loadFromFile loads configuration from a JSON file.
func loadFromFile(path string) (*GatewayAccountConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway account config file: %w", err)
	}

	var mappings map[string]*GatewayAccountMapping
	if err := json.Unmarshal(data, &mappings); err != nil {
		return nil, fmt.Errorf("failed to parse gateway account config file: %w", err)
	}

	config := &GatewayAccountConfig{
		Mappings: mappings,
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid gateway account config: %w", err)
	}

	return config, nil
}

// loadFromEnv loads configuration from environment variables.
// Looks for GATEWAY_{ID}_ACCOUNT_ID and GATEWAY_{ID}_ACCOUNT_TYPE variables.
func loadFromEnv() (*GatewayAccountConfig, error) {
	mappings := make(map[string]*GatewayAccountMapping)

	// Scan environment variables for gateway configurations
	// Format: GATEWAY_{ID}_ACCOUNT_ID and GATEWAY_{ID}_ACCOUNT_TYPE
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]

		// Look for GATEWAY_*_ACCOUNT_ID pattern
		if strings.HasPrefix(key, "GATEWAY_") && strings.HasSuffix(key, "_ACCOUNT_ID") {
			// Extract gateway ID: GATEWAY_STRIPE_ACCOUNT_ID -> STRIPE -> stripe
			gatewayID := extractGatewayID(key, "_ACCOUNT_ID")
			if gatewayID == "" {
				continue
			}

			accountID := parts[1]
			accountType := os.Getenv(fmt.Sprintf("GATEWAY_%s_ACCOUNT_TYPE", strings.ToUpper(gatewayID)))
			if accountType == "" {
				accountType = AccountTypeNostro // Default to NOSTRO if not specified
			}

			mappings[gatewayID] = &GatewayAccountMapping{
				GatewayID:       gatewayID,
				ContraAccountID: accountID,
				AccountType:     accountType,
			}
		}
	}

	if len(mappings) == 0 {
		return nil, ErrEmptyConfig
	}

	config := &GatewayAccountConfig{
		Mappings: mappings,
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid gateway account config: %w", err)
	}

	return config, nil
}

// extractGatewayID extracts the gateway ID from an environment variable key.
// e.g., GATEWAY_STRIPE_ACCOUNT_ID with suffix _ACCOUNT_ID returns "stripe"
func extractGatewayID(key, suffix string) string {
	// Remove "GATEWAY_" prefix and suffix
	trimmed := strings.TrimPrefix(key, "GATEWAY_")
	trimmed = strings.TrimSuffix(trimmed, suffix)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(trimmed)
}

// NewGatewayAccountConfig creates a new GatewayAccountConfig with the given mappings.
// This is useful for testing or programmatic configuration.
func NewGatewayAccountConfig(mappings map[string]*GatewayAccountMapping) (*GatewayAccountConfig, error) {
	config := &GatewayAccountConfig{
		Mappings: mappings,
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}
