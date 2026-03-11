package connector_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/identity/connector"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildClaims_FullIdentity(t *testing.T) {
	tid, err := tenant.NewTenantID("volterra")
	require.NoError(t, err)

	id := connector.Identity{
		UserID:   "user-uuid-123",
		Username: "Alice",
		Email:    "alice@example.com",
		Groups:   []string{"ADMIN", "OPERATOR"},
	}

	claims := connector.BuildClaims(id, tid)

	assert.Equal(t, "user-uuid-123", claims["sub"])
	assert.Equal(t, "alice@example.com", claims["email"])
	assert.Equal(t, "Alice", claims["name"])
	assert.Equal(t, "volterra", claims["x-tenant-id"])
	assert.Equal(t, []string{"admin", "operator"}, claims["roles"])
	assert.Equal(t, []string{"admin", "operator"}, claims["groups"])
}

func TestBuildClaims_EmptyUsernameDefaultsToEmail(t *testing.T) {
	tid, err := tenant.NewTenantID("acme")
	require.NoError(t, err)

	id := connector.Identity{
		UserID: "user-uuid-456",
		Email:  "bob@example.com",
		// Username intentionally empty
	}

	claims := connector.BuildClaims(id, tid)

	assert.Equal(t, "bob@example.com", claims["name"])
}

func TestBuildClaims_NilGroupsProducesEmptySlice(t *testing.T) {
	tid, err := tenant.NewTenantID("demo")
	require.NoError(t, err)

	id := connector.Identity{
		UserID: "user-uuid-789",
		Email:  "carol@example.com",
		Groups: nil,
	}

	claims := connector.BuildClaims(id, tid)

	roles, ok := claims["roles"].([]string)
	require.True(t, ok)
	assert.Empty(t, roles)
}

func TestBuildClaims_RoleNormalization(t *testing.T) {
	tid, err := tenant.NewTenantID("volterra")
	require.NoError(t, err)

	cases := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "UPPERCASE input produces lowercase output",
			input:    []string{"ADMIN", "OPERATOR"},
			expected: []string{"admin", "operator"},
		},
		{
			name:     "Mixed-Case input produces lowercase output",
			input:    []string{"Admin", "ReadOnly"},
			expected: []string{"admin", "readonly"},
		},
		{
			name:     "already-lowercase input is unchanged",
			input:    []string{"admin", "viewer"},
			expected: []string{"admin", "viewer"},
		},
		{
			name:     "empty roles produces empty slice",
			input:    []string{},
			expected: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := connector.Identity{
				UserID: "user-uuid-norm",
				Email:  "norm@example.com",
				Groups: tc.input,
			}

			claims := connector.BuildClaims(id, tid)

			assert.Equal(t, tc.expected, claims["roles"])
			assert.Equal(t, tc.expected, claims["groups"])
		})
	}
}

func TestBuildClaims_TenantIDPropagated(t *testing.T) {
	tid, err := tenant.NewTenantID("tenant_xyz")
	require.NoError(t, err)

	id := connector.Identity{UserID: "u1", Email: "e@e.com"}

	claims := connector.BuildClaims(id, tid)

	assert.Equal(t, "tenant_xyz", claims["x-tenant-id"])
}
