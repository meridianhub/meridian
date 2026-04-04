package provisioner

import "errors"

// Provisioner errors.
var (
	// ErrProvisioningStatusNotFound indicates no provisioning record exists for the tenant.
	// This occurs when:
	//  - The tenant has never been provisioned
	//  - The tenant was fully deprovisioned (status record removed)
	ErrProvisioningStatusNotFound = errors.New("provisioning status not found")

	// ErrProvisioningInProgress indicates provisioning is already running for this tenant.
	// Wait for the current provisioning to complete before retrying.
	ErrProvisioningInProgress = errors.New("provisioning already in progress")

	// ErrSchemaCreationFailed indicates the org_{tenant_id} schema could not be created.
	// Check PostgreSQL permissions and connection.
	ErrSchemaCreationFailed = errors.New("failed to create tenant schema")

	// ErrMigrationFailed indicates one or more service migrations failed.
	// Check ServiceSchemaStatus.ErrorMessage for details.
	ErrMigrationFailed = errors.New("failed to apply service migrations")

	// ErrDeprovisioningFailed indicates the schema could not be dropped.
	// The schema may contain objects owned by other roles.
	ErrDeprovisioningFailed = errors.New("failed to deprovision tenant schema")

	// ErrInvalidTenantID indicates the tenant ID doesn't meet naming requirements.
	// Tenant IDs must be alphanumeric with underscores, 1-50 characters.
	ErrInvalidTenantID = errors.New("invalid tenant ID format")

	// ErrProvisioningTimeout indicates the provisioning operation exceeded the configured timeout.
	ErrProvisioningTimeout = errors.New("provisioning operation timed out")

	// ErrNotDeprovisioned indicates an operation requires the tenant to be deprovisioned first.
	// For example, PurgeSchemas requires the tenant to be in 'deprovisioned' state.
	ErrNotDeprovisioned = errors.New("tenant must be deprovisioned before this operation")

	// ErrRetentionPeriodNotElapsed indicates the data retention period has not yet passed.
	// Schema data cannot be purged until the retention period (e.g., 7 years) has elapsed.
	ErrRetentionPeriodNotElapsed = errors.New("data retention period has not elapsed")

	// ErrAlreadyDeprovisioned indicates the tenant has already been deprovisioned.
	// This is informational - deprovisioning is idempotent.
	ErrAlreadyDeprovisioned = errors.New("tenant is already deprovisioned")

	// Config validation errors.

	// ErrNoServicesConfigured indicates no services were configured for provisioning.
	ErrNoServicesConfigured = errors.New("at least one service must be configured")

	// ErrInvalidProvisioningTimeout indicates the provisioning timeout is not positive.
	ErrInvalidProvisioningTimeout = errors.New("provisioning timeout must be positive")

	// ErrInvalidRetentionPeriod indicates the data retention period is negative.
	ErrInvalidRetentionPeriod = errors.New("data retention period cannot be negative")

	// ErrEmptyServiceName indicates a service has an empty name.
	ErrEmptyServiceName = errors.New("service has empty name")

	// ErrEmptyMigrationPath indicates a service has an empty migration path.
	ErrEmptyMigrationPath = errors.New("service has empty migration path")

	// ErrServiceDatabaseNotFound indicates a service database connection was not found.
	// This occurs when the service name in config doesn't match a database connection.
	ErrServiceDatabaseNotFound = errors.New("service database not found")

	// ErrCloseConnections indicates one or more database connections failed to close.
	ErrCloseConnections = errors.New("failed to close database connections")

	// ErrNilPlatformDB indicates the platformDB parameter was nil.
	ErrNilPlatformDB = errors.New("platformDB cannot be nil")

	// ErrCircuitBreakerOpen indicates the circuit breaker for a service is open.
	// This occurs when a service has experienced too many recent failures.
	// The service will be skipped until the circuit breaker transitions to half-open.
	ErrCircuitBreakerOpen = errors.New("circuit breaker open for service")

	// ErrCircuitBreakerTooManyRequests indicates too many requests are being
	// sent to a service in half-open state. Wait for the test requests to complete.
	ErrCircuitBreakerTooManyRequests = errors.New("circuit breaker rejecting requests")

	// ErrHookPanic indicates a post-provisioning hook panicked.
	// The recovered panic value is wrapped in the error chain.
	ErrHookPanic = errors.New("post-provisioning hook panicked")

	// ErrSchemaVerificationFailed indicates that post-migration table verification failed.
	// The tenant schema exists but expected tables were not created, indicating
	// migrations ran but did not produce the expected database objects.
	ErrSchemaVerificationFailed = errors.New("schema provisioning verification failed: expected tables not found")
)
