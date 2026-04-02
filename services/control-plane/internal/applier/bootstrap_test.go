package applier

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEmbeddedApplyManifest(t *testing.T) {
	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)

	assert.NotEmpty(t, script)
	assert.Equal(t, "1.5.0", version)
	assert.Contains(t, script, "apply_manifest")
	assert.Contains(t, script, "execute_apply_manifest")
	assert.Contains(t, script, "reference_data.register_instrument")
	assert.Contains(t, script, "reference_data.activate_instrument")
	assert.Contains(t, script, "internal_account.initiate")
	assert.Contains(t, script, "operational_gateway.upsert_connection")
	assert.Contains(t, script, "operational_gateway.upsert_route")
	assert.Contains(t, script, "market_information.register_data_source")
	assert.Contains(t, script, "market_information.register_data_set")
	assert.Contains(t, script, "market_information.activate_data_set")
	assert.Contains(t, script, "party.register_organization")
}

func TestIsSemverGreater(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{"empty b always returns true", "1.0.0", "", true},
		{"same version", "1.0.0", "1.0.0", false},
		{"major greater", "2.0.0", "1.0.0", true},
		{"major less", "1.0.0", "2.0.0", false},
		{"minor greater", "1.2.0", "1.1.0", true},
		{"minor less", "1.1.0", "1.2.0", false},
		{"patch greater", "1.0.2", "1.0.1", true},
		{"patch less", "1.0.1", "1.0.2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSemverGreater(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewBootstrap(t *testing.T) {
	b := NewBootstrap(nil)
	require.NotNil(t, b)
	assert.NotNil(t, b.logger)
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"100", 100},
		{"", 0},
		{"abc", 0},
		{"1a2b3", 123}, // parseInt extracts all digits; versions are pre-split by "."
		{"v2", 2},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseInt(tt.input))
		})
	}
}

func TestVersionFilenamePattern(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
		version string
	}{
		{"valid version", "v1.0.0.star", true, "1.0.0"},
		{"valid higher version", "v2.10.3.star", true, "2.10.3"},
		{"no v prefix", "1.0.0.star", false, ""},
		{"wrong extension", "v1.0.0.txt", false, ""},
		{"directory name", "v1.0.0", false, ""},
		{"non-numeric", "vx.y.z.star", false, ""},
		{"partial version", "v1.0.star", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := versionFilenamePattern.FindStringSubmatch(tt.input)
			if tt.matches {
				require.NotNil(t, matches)
				assert.Equal(t, tt.version, matches[1])
			} else {
				assert.Nil(t, matches)
			}
		})
	}
}

func TestIsSemverGreater_EqualVersions(t *testing.T) {
	// Equal versions should return false
	assert.False(t, isSemverGreater("1.0.0", "1.0.0"))
	assert.False(t, isSemverGreater("0.0.0", "0.0.0"))
	assert.False(t, isSemverGreater("99.99.99", "99.99.99"))
}

func TestIsSemverGreater_EmptyB(t *testing.T) {
	// When b is empty, always returns true (any version is greater than nothing)
	assert.True(t, isSemverGreater("", ""))
	assert.True(t, isSemverGreater("0.0.0", ""))
}

// newPlatformSagaPool creates a pgxpool.Pool backed by a PostgreSQL testcontainer
// with the platform_saga_definition table already created.
func newPlatformSagaPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testdb.NewTestPool(t)

	_, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS public.platform_saga_definition (
			id           uuid         NOT NULL,
			name         varchar(64)  NOT NULL,
			version      varchar(16)  NOT NULL,
			script       text         NOT NULL,
			status       varchar(16)  NOT NULL DEFAULT 'ACTIVE',
			display_name varchar(128),
			description  text,
			created_at   timestamptz  NOT NULL DEFAULT now(),
			updated_at   timestamptz  NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			CONSTRAINT chk_platform_saga_definition_version
				CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
			CONSTRAINT chk_platform_saga_definition_script_length
				CHECK (length(script) <= 65536)
		);
		CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_saga_definition_name_version
			ON public.platform_saga_definition (name, version);
	`)
	require.NoError(t, err)

	return pool
}

func TestEnsurePlatformSaga_InsertsNewRow(t *testing.T) {
	pool := newPlatformSagaPool(t)

	b := NewBootstrap(pool)
	err := b.EnsurePlatformSaga(context.Background())
	require.NoError(t, err)

	var count int
	err = pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM public.platform_saga_definition WHERE name = 'apply_manifest'`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestEnsurePlatformSaga_Idempotent(t *testing.T) {
	pool := newPlatformSagaPool(t)

	b := NewBootstrap(pool)

	// First call inserts the row.
	err := b.EnsurePlatformSaga(context.Background())
	require.NoError(t, err)

	// Second call finds the existing row and returns nil without inserting.
	err = b.EnsurePlatformSaga(context.Background())
	require.NoError(t, err)

	// Only one row should exist.
	var count int
	err = pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM public.platform_saga_definition WHERE name = 'apply_manifest'`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestLoadEmbeddedApplyManifest_ReturnsLatestVersion(t *testing.T) {
	// The embedded defaults contain v1.0.0, v1.1.0, v1.2.0, v1.3.0
	// loadEmbeddedApplyManifest should return v1.3.0 as the latest
	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)
	assert.Equal(t, "1.5.0", version)
	assert.NotEmpty(t, script)
	// Verify TrimSpace was applied: no leading whitespace
	assert.NotEqual(t, ' ', rune(script[0]))
}

func TestErrNoEmbeddedScript(t *testing.T) {
	assert.EqualError(t, ErrNoEmbeddedScript, "embedded apply_manifest saga script not found")
}
