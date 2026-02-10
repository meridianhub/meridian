package stripe

import "errors"

// TenantConfig holds the Stripe Connect configuration for a specific tenant.
type TenantConfig struct {
	// ConnectedAccountID is the Stripe Connected Account ID (acct_...).
	ConnectedAccountID string

	// WebhookEndpointSecret is the per-tenant webhook signing secret (whsec_...).
	WebhookEndpointSecret string
}

// Validate checks that required fields are present.
func (tc TenantConfig) Validate() error {
	if tc.ConnectedAccountID == "" {
		return ErrMissingAccountID
	}
	return nil
}

// TenantConfigProvider retrieves Stripe Connect configuration for a tenant.
// Implementations may call control-plane gRPC, read from local config, etc.
type TenantConfigProvider interface {
	// GetTenantConfig returns the Stripe Connect config for the given tenant.
	// Returns ErrTenantConfigNotFound if the tenant has no Stripe configuration.
	GetTenantConfig(tenantID string) (TenantConfig, error)
}

// Tenant config errors.
var (
	ErrMissingAccountID     = errors.New("connected account ID must not be empty")
	ErrTenantConfigNotFound = errors.New("stripe configuration not found for tenant")
)
