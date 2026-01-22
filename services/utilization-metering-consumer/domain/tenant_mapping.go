// Package domain provides core domain logic for the utilization-metering-consumer service.
package domain

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// ParseTenantAccountMapping parses a JSON string mapping tenant UUIDs to account UUIDs.
// Expected format: {"tenant_id_1": "account_id_1", "tenant_id_2": "account_id_2"}
// Returns an empty map if the input string is empty.
func ParseTenantAccountMapping(jsonStr string) (map[uuid.UUID]uuid.UUID, error) {
	if jsonStr == "" {
		return make(map[uuid.UUID]uuid.UUID), nil
	}

	var rawMap map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &rawMap); err != nil {
		return nil, fmt.Errorf("failed to parse tenant account mapping JSON: %w", err)
	}

	result := make(map[uuid.UUID]uuid.UUID)
	for tenantIDStr, accountIDStr := range rawMap {
		tenantID, err := uuid.Parse(tenantIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid tenant_id '%s': %w", tenantIDStr, err)
		}
		accountID, err := uuid.Parse(accountIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid account_id '%s' for tenant %s: %w", accountIDStr, tenantIDStr, err)
		}
		result[tenantID] = accountID
	}

	return result, nil
}
