package provisioner

import "errors"

// Provisioner errors.
var (
	// ErrTestDatabaseConnectionFailed is a test error for simulating database failures.
	ErrTestDatabaseConnectionFailed = errors.New("test: database connection failed")

	// ErrTestGeneric is a test error for simulating generic failures.
	ErrTestGeneric = errors.New("test: generic error")
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
)
