package saga

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/pkg/saga/validation"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// setupTestPostgres creates a unique tenant schema in the shared PostgreSQL container
// for test isolation. Each test gets its own schema with fresh tables.
func setupTestPostgres(t *testing.T) (*pgxpool.Pool, tenant.TenantID, func()) {
	ctx := context.Background()
	pool := sharedPgPool

	// Create a unique tenant ID per test to avoid cross-test interference
	suffix := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	// Replace any non-alphanumeric chars (tenant IDs only allow [a-z0-9_])
	suffix = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, suffix)
	if len(suffix) > 30 {
		suffix = suffix[:30]
	}
	tenantName := fmt.Sprintf("t_%s_%s", suffix, strings.ReplaceAll(uuid.New().String(), "-", "")[:8])
	tenantID, err := tenant.NewTenantID(tenantName)
	require.NoError(t, err)

	schemaName := tenantID.SchemaName()
	_, err = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName)
	require.NoError(t, err)

	// Create platform_saga_definition in public schema (needed for LEFT JOINs in queries)
	platformTableSQL := `
		CREATE TABLE IF NOT EXISTS public.platform_saga_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			name varchar(64) NOT NULL,
			version varchar(16) NOT NULL,
			script text NOT NULL,
			display_name varchar(128) NULL,
			description text NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			CONSTRAINT uq_platform_saga_definition_name UNIQUE (name)
		)`
	_, err = pool.Exec(ctx, platformTableSQL)
	require.NoError(t, err)

	createTableSQL := `
		CREATE TABLE ` + schemaName + `.saga_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			name varchar(64) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			script text NULL,
			status varchar(16) NOT NULL DEFAULT 'DRAFT',
			is_system boolean NOT NULL DEFAULT FALSE,
			preconditions_expression text NULL,
			display_name varchar(128) NULL,
			description text NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			activated_at timestamptz NULL,
			deprecated_at timestamptz NULL,
			successor_id uuid NULL,
			platform_ref uuid NULL,
			override_reason text NULL,
			platform_version_at_override varchar(16) NULL,
			validation_status text NOT NULL DEFAULT 'UNVALIDATED',
			complexity_score integer NULL,
			handler_call_count integer NULL,
			validated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_saga_definition_name_version UNIQUE (name, version),
			CONSTRAINT fk_saga_definition_platform_ref
				FOREIGN KEY (platform_ref) REFERENCES public.platform_saga_definition (id) ON DELETE SET NULL,
			CONSTRAINT chk_saga_definition_script_source
				CHECK (NOT (platform_ref IS NOT NULL AND script IS NOT NULL AND script != ''))
		)`
	_, err = pool.Exec(ctx, createTableSQL)
	require.NoError(t, err)

	cleanup := func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE")
	}

	return pool, tenantID, cleanup
}

func TestRegistryHandler_CreateSagaDraft(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("creates draft saga successfully", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:        "test_saga",
			Script:      `saga(name="test_saga")`,
			DisplayName: "Test Saga",
			Description: "A test saga for unit testing",
		}

		resp, err := handler.CreateSagaDraft(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Saga)

		assert.Equal(t, "test_saga", resp.Saga.Name)
		assert.Equal(t, int32(1), resp.Saga.Version)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DRAFT, resp.Saga.Status)
		assert.Equal(t, `saga(name="test_saga")`, resp.Saga.Script)
		assert.False(t, resp.Saga.IsSystem)
		assert.NotEmpty(t, resp.Saga.Id)
		assert.NotNil(t, resp.Saga.CreatedAt)
	})

	t.Run("creates draft with specific version", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:    "versioned_saga",
			Version: 2,
			Script:  `saga(name="versioned_saga")`,
		}

		resp, err := handler.CreateSagaDraft(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.Saga.Version)
	})

	t.Run("rejects duplicate name+version", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:   "test_saga",
			Script: `saga(name="test_saga")`,
		}

		_, err := handler.CreateSagaDraft(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})
}

func TestRegistryHandler_UpdateSagaDefinition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Create a draft first
	createReq := &sagav1.CreateSagaDraftRequest{
		Name:   "updatable_saga",
		Script: `saga(name="updatable_saga")`,
	}
	createResp, err := handler.CreateSagaDraft(ctx, createReq)
	require.NoError(t, err)

	sagaID := createResp.Saga.Id

	t.Run("updates draft successfully", func(t *testing.T) {
		req := &sagav1.UpdateSagaDefinitionRequest{
			Id:          sagaID,
			Script:      proto.String(`saga(name="updatable_saga", version="2.0")`),
			DisplayName: proto.String("Updated Saga"),
			Description: proto.String("Updated description"),
		}

		resp, err := handler.UpdateSagaDefinition(ctx, req)
		require.NoError(t, err)

		assert.Equal(t, `saga(name="updatable_saga", version="2.0")`, resp.Saga.Script)
		assert.Equal(t, "Updated Saga", resp.Saga.DisplayName)
		assert.Equal(t, "Updated description", resp.Saga.Description)
	})

	t.Run("rejects invalid saga id", func(t *testing.T) {
		req := &sagav1.UpdateSagaDefinitionRequest{
			Id:     "not-a-uuid",
			Script: proto.String(`saga(name="test")`),
		}

		_, err := handler.UpdateSagaDefinition(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("rejects non-existent saga", func(t *testing.T) {
		req := &sagav1.UpdateSagaDefinitionRequest{
			Id:     uuid.New().String(),
			Script: proto.String(`saga(name="test")`),
		}

		_, err := handler.UpdateSagaDefinition(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestRegistryHandler_ActivateSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Create a draft first
	createReq := &sagav1.CreateSagaDraftRequest{
		Name:   "activatable_saga",
		Script: `saga(name="activatable_saga")`,
	}
	createResp, err := handler.CreateSagaDraft(ctx, createReq)
	require.NoError(t, err)

	sagaID := createResp.Saga.Id

	t.Run("activates saga successfully", func(t *testing.T) {
		req := &sagav1.ActivateSagaRequest{
			Id: sagaID,
		}

		resp, err := handler.ActivateSaga(ctx, req)
		require.NoError(t, err)

		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_ACTIVE, resp.Saga.Status)
		assert.NotNil(t, resp.Saga.ActivatedAt)
	})

	t.Run("activation of already active saga is idempotent", func(t *testing.T) {
		req := &sagav1.ActivateSagaRequest{
			Id: sagaID,
		}

		resp, err := handler.ActivateSaga(ctx, req)
		require.NoError(t, err)

		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_ACTIVE, resp.Saga.Status)
	})
}

func TestRegistryHandler_DeprecateSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Create and activate a saga
	createReq := &sagav1.CreateSagaDraftRequest{
		Name:   "deprecatable_saga",
		Script: `saga(name="deprecatable_saga")`,
	}
	createResp, err := handler.CreateSagaDraft(ctx, createReq)
	require.NoError(t, err)

	_, err = handler.ActivateSaga(ctx, &sagav1.ActivateSagaRequest{Id: createResp.Saga.Id})
	require.NoError(t, err)

	t.Run("deprecates saga successfully", func(t *testing.T) {
		req := &sagav1.DeprecateSagaRequest{
			Id: createResp.Saga.Id,
		}

		resp, err := handler.DeprecateSaga(ctx, req)
		require.NoError(t, err)

		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DEPRECATED, resp.Saga.Status)
		assert.NotNil(t, resp.Saga.DeprecatedAt)
	})

	t.Run("rejects deprecation of non-active saga", func(t *testing.T) {
		// Create a draft saga
		draftReq := &sagav1.CreateSagaDraftRequest{
			Name:   "draft_saga",
			Script: `saga(name="draft_saga")`,
		}
		draftResp, err := handler.CreateSagaDraft(ctx, draftReq)
		require.NoError(t, err)

		req := &sagav1.DeprecateSagaRequest{
			Id: draftResp.Saga.Id,
		}

		_, err = handler.DeprecateSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestRegistryHandler_GetSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Create a saga
	createReq := &sagav1.CreateSagaDraftRequest{
		Name:        "get_saga_test",
		Script:      `saga(name="get_saga_test")`,
		DisplayName: "Get Saga Test",
	}
	createResp, err := handler.CreateSagaDraft(ctx, createReq)
	require.NoError(t, err)

	t.Run("gets saga by id", func(t *testing.T) {
		req := &sagav1.GetSagaRequest{
			Id: createResp.Saga.Id,
		}

		resp, err := handler.GetSaga(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "get_saga_test", resp.Saga.Name)
	})

	t.Run("gets saga by name and version", func(t *testing.T) {
		req := &sagav1.GetSagaRequest{
			Name:    "get_saga_test",
			Version: 1,
		}

		resp, err := handler.GetSaga(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "get_saga_test", resp.Saga.Name)
	})

	t.Run("requires either id or name", func(t *testing.T) {
		req := &sagav1.GetSagaRequest{}

		_, err := handler.GetSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestRegistryHandler_GetActiveSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Create and activate a saga
	createReq := &sagav1.CreateSagaDraftRequest{
		Name:   "active_saga",
		Script: `saga(name="active_saga")`,
	}
	createResp, err := handler.CreateSagaDraft(ctx, createReq)
	require.NoError(t, err)

	_, err = handler.ActivateSaga(ctx, &sagav1.ActivateSagaRequest{Id: createResp.Saga.Id})
	require.NoError(t, err)

	t.Run("gets active saga by name", func(t *testing.T) {
		req := &sagav1.GetActiveSagaRequest{
			Name: "active_saga",
		}

		resp, err := handler.GetActiveSaga(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "active_saga", resp.Saga.Name)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_ACTIVE, resp.Saga.Status)
		assert.True(t, resp.IsTenantOverride) // Not a system saga
	})

	t.Run("returns not found for non-existent saga", func(t *testing.T) {
		req := &sagav1.GetActiveSagaRequest{
			Name: "non_existent",
		}

		_, err := handler.GetActiveSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestRegistryHandler_ListSagas(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Create multiple sagas
	for i := 0; i < 3; i++ {
		createReq := &sagav1.CreateSagaDraftRequest{
			Name:   "list_saga_" + string(rune('a'+i)),
			Script: `saga(name="list_saga")`,
		}
		_, err := handler.CreateSagaDraft(ctx, createReq)
		require.NoError(t, err)
	}

	t.Run("lists all sagas", func(t *testing.T) {
		req := &sagav1.ListSagasRequest{
			ExcludeSystem: false,
		}

		resp, err := handler.ListSagas(ctx, req)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(resp.Sagas), 3)
	})

	t.Run("filters by status", func(t *testing.T) {
		req := &sagav1.ListSagasRequest{
			StatusFilter:  sagav1.SagaStatus_SAGA_STATUS_DRAFT,
			ExcludeSystem: false,
		}

		resp, err := handler.ListSagas(ctx, req)
		require.NoError(t, err)

		for _, saga := range resp.Sagas {
			assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DRAFT, saga.Status)
		}
	})

	t.Run("respects page size", func(t *testing.T) {
		req := &sagav1.ListSagasRequest{
			PageSize:      2,
			ExcludeSystem: false,
		}

		resp, err := handler.ListSagas(ctx, req)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(resp.Sagas), 2)
	})
}

func TestRegistryHandler_SagaLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("full lifecycle: create -> update -> activate -> deprecate", func(t *testing.T) {
		// Step 1: Create draft
		createResp, err := handler.CreateSagaDraft(ctx, &sagav1.CreateSagaDraftRequest{
			Name:        "lifecycle_saga",
			Script:      `saga(name="lifecycle_saga")`,
			DisplayName: "Lifecycle Test Saga",
		})
		require.NoError(t, err)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DRAFT, createResp.Saga.Status)

		sagaID := createResp.Saga.Id

		// Step 2: Update draft
		updateResp, err := handler.UpdateSagaDefinition(ctx, &sagav1.UpdateSagaDefinitionRequest{
			Id:          sagaID,
			Script:      proto.String(`saga(name="lifecycle_saga", version="1.1")`),
			Description: proto.String("Updated description"),
		})
		require.NoError(t, err)
		assert.Contains(t, updateResp.Saga.Script, "version=\"1.1\"")

		// Step 3: Activate
		activateResp, err := handler.ActivateSaga(ctx, &sagav1.ActivateSagaRequest{
			Id: sagaID,
		})
		require.NoError(t, err)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_ACTIVE, activateResp.Saga.Status)
		assert.NotNil(t, activateResp.Saga.ActivatedAt)

		// Step 4: Try to update (should fail - not in draft)
		_, err = handler.UpdateSagaDefinition(ctx, &sagav1.UpdateSagaDefinitionRequest{
			Id:     sagaID,
			Script: proto.String(`saga(name="lifecycle_saga", version="1.2")`),
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.FailedPrecondition, st.Code())

		// Step 5: Deprecate
		deprecateResp, err := handler.DeprecateSaga(ctx, &sagav1.DeprecateSagaRequest{
			Id: sagaID,
		})
		require.NoError(t, err)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DEPRECATED, deprecateResp.Saga.Status)
		assert.NotNil(t, deprecateResp.Saga.DeprecatedAt)
	})
}

func TestRegistryHandler_TenantOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenantID)
	schemaName := tenantID.SchemaName()

	// Seed a system saga
	_, err := pool.Exec(ctx, `
		INSERT INTO `+schemaName+`.saga_definition
			(name, version, script, status, is_system, display_name, activated_at)
		VALUES
			('system_saga', 1, 'saga(name="system_saga")', 'ACTIVE', true, 'System Saga', now())`)
	require.NoError(t, err)

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)

	t.Run("tenant override takes precedence over system saga", func(t *testing.T) {
		// Create and activate a tenant override
		createResp, err := handler.CreateSagaDraft(ctx, &sagav1.CreateSagaDraftRequest{
			Name:   "system_saga",
			Script: `saga(name="system_saga", tenant_override=True)`,
		})
		require.NoError(t, err)

		_, err = handler.ActivateSaga(ctx, &sagav1.ActivateSagaRequest{
			Id: createResp.Saga.Id,
		})
		require.NoError(t, err)

		// GetActiveSaga should return the tenant override
		activeResp, err := handler.GetActiveSaga(ctx, &sagav1.GetActiveSagaRequest{
			Name: "system_saga",
		})
		require.NoError(t, err)

		assert.True(t, activeResp.IsTenantOverride)
		assert.Contains(t, activeResp.Saga.Script, "tenant_override")
	})

	t.Run("cannot modify system saga", func(t *testing.T) {
		// Try to get the system saga's ID
		var systemID string
		err := pool.QueryRow(ctx, `
			SELECT id FROM `+schemaName+`.saga_definition
			WHERE name = 'system_saga' AND is_system = true`).Scan(&systemID)
		require.NoError(t, err)

		// Try to update it
		_, err = handler.UpdateSagaDefinition(ctx, &sagav1.UpdateSagaDefinitionRequest{
			Id:     systemID,
			Script: proto.String(`saga(name="modified_system")`),
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})
}

// setupTestDryRunValidator creates a DryRunValidator for testing with common handlers.
func setupTestDryRunValidator(t *testing.T) *validation.DryRunValidator {
	t.Helper()

	schemaRegistry := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test_service.test_method:
    description: Test method
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
      error:
        type: string
  payment.create_lien:
    description: Create payment lien
    compensation_strategy: none
    params:
      amount:
        type: string
        required: true
      customer_id:
        type: string
        required: true
    returns:
      lien_id:
        type: string
      error:
        type: string
`
	require.NoError(t, schemaRegistry.LoadFromYAML([]byte(schemaYAML)))

	validator, err := validation.NewMockValidatorForTesting(schemaRegistry)
	require.NoError(t, err)
	return validator
}

func TestRegistryHandler_CreateSagaDraft_WithDryRunValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	dryRunValidator := setupTestDryRunValidator(t)
	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, dryRunValidator, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("valid script registers successfully with validation metadata", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:        "validated_saga",
			Script:      `result = test_service.test_method()`,
			DisplayName: "Validated Saga",
		}

		resp, err := handler.CreateSagaDraft(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Saga)

		assert.Equal(t, "validated_saga", resp.Saga.Name)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DRAFT, resp.Saga.Status)

		// Verify validation metadata was stored by re-fetching from DB
		fetched, err := registry.GetByID(ctx, uuid.MustParse(resp.Saga.Id))
		require.NoError(t, err)

		assert.Equal(t, "PASSED", fetched.ValidationStatus)
		require.NotNil(t, fetched.ComplexityScore, "complexity score should be stored")
		assert.GreaterOrEqual(t, *fetched.ComplexityScore, 0)
		require.NotNil(t, fetched.HandlerCallCount, "handler call count should be stored")
		assert.Equal(t, 1, *fetched.HandlerCallCount)
		require.NotNil(t, fetched.ValidatedAt, "validated_at should be stored")
		assert.WithinDuration(t, time.Now(), *fetched.ValidatedAt, 5*time.Second)
	})

	t.Run("invalid script rejected with INVALID_ARGUMENT", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:   "invalid_saga",
			Script: `result = test_service.test_method(  # Missing closing paren`,
		}

		_, err := handler.CreateSagaDraft(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "saga script validation failed")
	})

	t.Run("runtime error in script rejected", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:   "runtime_error_saga",
			Script: `fail("intentional failure")`,
		}

		_, err := handler.CreateSagaDraft(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "saga script validation failed")
	})

	t.Run("undefined handler in script rejected", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:   "undefined_handler_saga",
			Script: `result = nonexistent_service.some_method()`,
		}

		_, err := handler.CreateSagaDraft(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("invalid script not persisted to database", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:   "should_not_persist",
			Script: `fail("should not be saved")`,
		}

		_, err := handler.CreateSagaDraft(ctx, req)
		require.Error(t, err)

		// Verify it was NOT persisted
		_, getErr := registry.GetDefinition(ctx, "should_not_persist", 1)
		assert.ErrorIs(t, getErr, ErrNotFound)
	})

	t.Run("multiple handler calls stores correct metrics", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name: "multi_handler_saga",
			Script: `
lien_result = payment.create_lien(amount="100.00", customer_id="cust_123")
result = test_service.test_method()
verify = test_service.test_method()
`,
		}

		resp, err := handler.CreateSagaDraft(ctx, req)
		require.NoError(t, err)

		fetched, err := registry.GetByID(ctx, uuid.MustParse(resp.Saga.Id))
		require.NoError(t, err)

		assert.Equal(t, "PASSED", fetched.ValidationStatus)
		require.NotNil(t, fetched.HandlerCallCount)
		assert.Equal(t, 3, *fetched.HandlerCallCount)
		require.NotNil(t, fetched.ComplexityScore)
		assert.Equal(t, 1, *fetched.ComplexityScore) // 3/2 = 1
	})
}

func TestRegistryHandler_CreateSagaDraft_WithoutDryRunValidator(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	// Handler without dryRunValidator - backward compatible
	handler := NewRegistryHandler(registry, nil, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("creates draft without validation when validator not configured", func(t *testing.T) {
		req := &sagav1.CreateSagaDraftRequest{
			Name:   "no_validation_saga",
			Script: `fail("this would fail validation but no validator configured")`,
		}

		resp, err := handler.CreateSagaDraft(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Saga)
		assert.Equal(t, "no_validation_saga", resp.Saga.Name)

		// Validation metadata should be default/empty
		fetched, err := registry.GetByID(ctx, uuid.MustParse(resp.Saga.Id))
		require.NoError(t, err)
		assert.Equal(t, "UNVALIDATED", fetched.ValidationStatus)
		assert.Nil(t, fetched.ComplexityScore)
		assert.Nil(t, fetched.HandlerCallCount)
		assert.Nil(t, fetched.ValidatedAt)
	})
}

func TestRegistryHandler_ExistingTests_StillPass_WithValidator(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	dryRunValidator := setupTestDryRunValidator(t)
	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, dryRunValidator, nil)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("full lifecycle works with validation enabled", func(t *testing.T) {
		// Create - valid script passes validation
		createResp, err := handler.CreateSagaDraft(ctx, &sagav1.CreateSagaDraftRequest{
			Name:        "lifecycle_validated",
			Script:      `test_service.test_method()`,
			DisplayName: "Lifecycle with validation",
		})
		require.NoError(t, err)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DRAFT, createResp.Saga.Status)

		sagaID := createResp.Saga.Id

		// Activate
		activateResp, err := handler.ActivateSaga(ctx, &sagav1.ActivateSagaRequest{
			Id: sagaID,
		})
		require.NoError(t, err)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_ACTIVE, activateResp.Saga.Status)

		// Deprecate
		deprecateResp, err := handler.DeprecateSaga(ctx, &sagav1.DeprecateSagaRequest{
			Id: sagaID,
		})
		require.NoError(t, err)
		assert.Equal(t, sagav1.SagaStatus_SAGA_STATUS_DEPRECATED, deprecateResp.Saga.Status)
	})
}

func TestRegistryHandler_CreateSagaDraft_NegativeVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)
	ctx := tenant.WithTenant(context.Background(), tenantID)

	req := &sagav1.CreateSagaDraftRequest{
		Name:    "negative_version_saga",
		Version: -1,
		Script:  `saga(name="negative_version_saga")`,
	}

	_, err := handler.CreateSagaDraft(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "version must be non-negative")
}

func TestRegistryHandler_DeprecateSaga_InvalidIDs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)
	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("invalid saga UUID", func(t *testing.T) {
		req := &sagav1.DeprecateSagaRequest{
			Id: "not-a-uuid",
		}

		_, err := handler.DeprecateSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "invalid saga id")
	})

	t.Run("invalid successor UUID", func(t *testing.T) {
		// Create and activate a saga so we have a valid saga ID
		createResp, err := handler.CreateSagaDraft(ctx, &sagav1.CreateSagaDraftRequest{
			Name:   "deprecate_invalid_successor",
			Script: `saga(name="deprecate_invalid_successor")`,
		})
		require.NoError(t, err)
		_, err = handler.ActivateSaga(ctx, &sagav1.ActivateSagaRequest{Id: createResp.Saga.Id})
		require.NoError(t, err)

		req := &sagav1.DeprecateSagaRequest{
			Id:          createResp.Saga.Id,
			SuccessorId: "not-a-uuid",
		}

		_, err = handler.DeprecateSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "invalid successor_id")
	})
}

func TestRegistryHandler_GetSaga_ErrorPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)
	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("invalid UUID", func(t *testing.T) {
		req := &sagav1.GetSagaRequest{
			Id: "not-a-uuid",
		}

		_, err := handler.GetSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("negative version", func(t *testing.T) {
		req := &sagav1.GetSagaRequest{
			Name:    "some_saga",
			Version: -1,
		}

		_, err := handler.GetSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "version must be >= 0")
	})

	t.Run("version zero triggers active lookup", func(t *testing.T) {
		// Version 0 means "get active version" — non-existent name returns NotFound
		req := &sagav1.GetSagaRequest{
			Name:    "nonexistent_active_saga",
			Version: 0,
		}

		_, err := handler.GetSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("non-existent saga by name", func(t *testing.T) {
		req := &sagav1.GetSagaRequest{
			Name:    "absolutely_does_not_exist",
			Version: 1,
		}

		_, err := handler.GetSaga(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestRegistryHandler_ListSagas_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	handler := NewRegistryHandler(registry, nil, nil, nil)
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Seed some sagas
	for i := 0; i < 5; i++ {
		_, err := handler.CreateSagaDraft(ctx, &sagav1.CreateSagaDraftRequest{
			Name:   "pagination_saga_" + strconv.Itoa(i),
			Script: `saga(name="pagination")`,
		})
		require.NoError(t, err)
	}

	t.Run("pageSize 0 defaults to 50", func(t *testing.T) {
		resp, err := handler.ListSagas(ctx, &sagav1.ListSagasRequest{
			PageSize: 0,
		})
		require.NoError(t, err)
		// With only 5 sagas, all should be returned (50 > 5) and no next token
		assert.GreaterOrEqual(t, len(resp.Sagas), 5)
		assert.Empty(t, resp.NextPageToken)
	})

	t.Run("pageSize over 100 caps to 100", func(t *testing.T) {
		resp, err := handler.ListSagas(ctx, &sagav1.ListSagasRequest{
			PageSize: 200,
		})
		require.NoError(t, err)
		// All 5 sagas fit within the capped page size of 100
		assert.GreaterOrEqual(t, len(resp.Sagas), 5)
		assert.Empty(t, resp.NextPageToken)
	})

	t.Run("invalid page_token", func(t *testing.T) {
		_, err := handler.ListSagas(ctx, &sagav1.ListSagasRequest{
			PageToken: "not-a-number",
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "invalid page_token")
	})

	t.Run("ExcludeSystem filter", func(t *testing.T) {
		// Insert a system saga directly
		schemaName := tenantID.SchemaName()
		_, err := pool.Exec(ctx, `
			INSERT INTO `+schemaName+`.saga_definition
				(name, version, script, status, is_system)
			VALUES
				('system_list_test', 1, 'saga(name="system")', 'DRAFT', true)`)
		require.NoError(t, err)

		// Without ExcludeSystem — system saga should be present
		allResp, err := handler.ListSagas(ctx, &sagav1.ListSagasRequest{
			ExcludeSystem: false,
		})
		require.NoError(t, err)
		hasSystem := false
		for _, s := range allResp.Sagas {
			if s.Name == "system_list_test" {
				hasSystem = true
				break
			}
		}
		assert.True(t, hasSystem, "system saga should be included when ExcludeSystem=false")

		// With ExcludeSystem — system saga should be excluded
		filteredResp, err := handler.ListSagas(ctx, &sagav1.ListSagasRequest{
			ExcludeSystem: true,
		})
		require.NoError(t, err)
		for _, s := range filteredResp.Sagas {
			assert.False(t, s.IsSystem, "no system saga should be returned when ExcludeSystem=true")
		}
	})
}

func TestRegistryHandler_DescribeHandlers(t *testing.T) {
	t.Run("no schema returns empty response", func(t *testing.T) {
		handler := NewRegistryHandler(nil, nil, nil, nil)

		resp, err := handler.DescribeHandlers(context.Background(), &sagav1.DescribeHandlersRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Services)
	})

	t.Run("with schema returns handlers grouped by service", func(t *testing.T) {
		reg := schema.NewRegistry()
		yamlData := []byte(`
service: test
version: 1.0
handlers:
  position_keeping.initiate_log:
    description: Initiate a position log entry
    compensation_strategy: none
    params:
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      log_id:
        type: string
  payment.create_lien:
    description: Create payment lien
    compensation_strategy: none
    params:
      amount:
        type: string
        required: true
    returns:
      lien_id:
        type: string
`)
		require.NoError(t, reg.LoadFromYAML(yamlData))

		handler := NewRegistryHandler(nil, nil, nil, nil).WithSchemaRegistry(reg)

		resp, err := handler.DescribeHandlers(context.Background(), &sagav1.DescribeHandlersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Services, 2)

		// Services should be sorted alphabetically
		assert.Equal(t, "payment", resp.Services[0].Name)
		assert.Equal(t, "position_keeping", resp.Services[1].Name)

		// Check payment service handlers
		require.Len(t, resp.Services[0].Handlers, 1)
		assert.Equal(t, "create_lien", resp.Services[0].Handlers[0].Name)
		assert.Equal(t, "Create payment lien", resp.Services[0].Handlers[0].Description)

		// Check position_keeping service handlers
		require.Len(t, resp.Services[1].Handlers, 1)
		assert.Equal(t, "initiate_log", resp.Services[1].Handlers[0].Name)
		require.Len(t, resp.Services[1].Handlers[0].Parameters, 2)
	})
}

func TestRegistryHandler_MapDomainError(t *testing.T) {
	handler := NewRegistryHandler(nil, nil, nil, nil)

	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"ErrNotFound", ErrNotFound, codes.NotFound},
		{"ErrSystemSagaReadOnly", ErrSystemSagaReadOnly, codes.PermissionDenied},
		{"ErrOptimisticLock", ErrOptimisticLock, codes.Aborted},
		{"ErrValidationFailed", ErrValidationFailed, codes.FailedPrecondition},
		{"ErrSuccessorInvalid", ErrSuccessorInvalid, codes.FailedPrecondition},
		{"ErrNotDraft", ErrNotDraft, codes.FailedPrecondition},
		{"ErrNotActive", ErrNotActive, codes.FailedPrecondition},
		{"ErrAlreadyExists", ErrAlreadyExists, codes.AlreadyExists},
		{"ErrInvalidStateTransition", ErrInvalidStateTransition, codes.FailedPrecondition},
		{"unknown error", errors.New("something unexpected"), codes.Internal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := handler.mapDomainError(tc.err, "TestOp", "test-id")
			st, ok := status.FromError(result)
			require.True(t, ok)
			assert.Equal(t, tc.expectedCode, st.Code())
		})
	}
}

func TestRegistryHandler_ValidateSagaDraft_ErrorPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool, tenantID, cleanup := setupTestPostgres(t)
	defer cleanup()

	registry := NewPostgresRegistry(pool, nil)
	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Run("invalid UUID", func(t *testing.T) {
		handler := NewRegistryHandler(registry, nil, nil, nil)

		_, err := handler.ValidateSagaDraft(ctx, &sagav1.ValidateSagaDraftRequest{
			Id: "not-a-uuid",
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("non-existent saga", func(t *testing.T) {
		handler := NewRegistryHandler(registry, nil, nil, nil)

		_, err := handler.ValidateSagaDraft(ctx, &sagav1.ValidateSagaDraftRequest{
			Id: uuid.New().String(),
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("no validator returns READY status", func(t *testing.T) {
		handler := NewRegistryHandler(registry, nil, nil, nil)

		// Create a saga to validate
		createResp, err := handler.CreateSagaDraft(ctx, &sagav1.CreateSagaDraftRequest{
			Name:   "validate_no_validator",
			Script: `saga(name="validate_no_validator")`,
		})
		require.NoError(t, err)

		resp, err := handler.ValidateSagaDraft(ctx, &sagav1.ValidateSagaDraftRequest{
			Id: createResp.Saga.Id,
		})
		require.NoError(t, err)
		assert.Equal(t, "READY", resp.Validation.Status)
		assert.Equal(t, "No validator configured", resp.Report)
	})
}

func TestRegistryHandler_AnalyzeDeprecationImpact_NoValidator(t *testing.T) {
	handler := NewRegistryHandler(nil, nil, nil, nil)

	resp, err := handler.AnalyzeDeprecationImpact(context.Background(), &sagav1.AnalyzeDeprecationImpactRequest{
		InstrumentCode: "GBP",
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Dependencies)
	assert.Equal(t, int32(0), resp.TotalCount)
}
