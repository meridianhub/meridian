package migrations_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/internal/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriverFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     migrations.Driver
	}{
		{
			name:     "empty env defaults to cockroachdb",
			envValue: "",
			want:     migrations.DriverCockroachDB,
		},
		{
			name:     "cockroachdb explicit",
			envValue: "cockroachdb",
			want:     migrations.DriverCockroachDB,
		},
		{
			name:     "postgres lowercase",
			envValue: "postgres",
			want:     migrations.DriverPostgres,
		},
		{
			name:     "postgresql alias",
			envValue: "postgresql",
			want:     migrations.DriverPostgres,
		},
		{
			name:     "POSTGRES uppercase",
			envValue: "POSTGRES",
			want:     migrations.DriverPostgres,
		},
		{
			name:     "unknown value defaults to cockroachdb",
			envValue: "mysql",
			want:     migrations.DriverCockroachDB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DB_DRIVER", tt.envValue)
			got := migrations.DriverFromEnv()
			if got != tt.want {
				t.Errorf("DriverFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildServiceDSN_DefaultPort(t *testing.T) {
	sdb := migrations.ServiceDatabase{
		Database: "meridian_party",
		User:     "meridian_party_user",
		Password: "",
	}

	tests := []struct {
		name         string
		superuserDSN string
		driver       migrations.Driver
		wantPort     string
	}{
		{
			name:         "cockroachdb uses port 26257 when no port in DSN",
			superuserDSN: "postgres://root@localhost/defaultdb?sslmode=disable",
			driver:       migrations.DriverCockroachDB,
			wantPort:     "26257",
		},
		{
			name:         "postgres uses port 5432 when no port in DSN",
			superuserDSN: "postgres://postgres@localhost/defaultdb?sslmode=disable",
			driver:       migrations.DriverPostgres,
			wantPort:     "5432",
		},
		{
			name:         "explicit port preserved regardless of driver",
			superuserDSN: "postgres://root@localhost:9999/defaultdb?sslmode=disable",
			driver:       migrations.DriverPostgres,
			wantPort:     "9999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := migrations.BuildServiceDSN(tt.superuserDSN, sdb, tt.driver)
			if !strings.Contains(got, ":"+tt.wantPort+"/") {
				t.Errorf("BuildServiceDSN() = %q, want port %q in DSN", got, tt.wantPort)
			}
		})
	}
}

func TestBuildServiceDSN_DatabaseAndUser(t *testing.T) {
	sdb := migrations.ServiceDatabase{
		Database: "meridian_party",
		User:     "meridian_party_user",
		Password: "secret",
	}

	got := migrations.BuildServiceDSN("postgres://root@localhost:26257/defaultdb?sslmode=disable", sdb, migrations.DriverCockroachDB)

	if !strings.Contains(got, "meridian_party_user:secret@") {
		t.Errorf("expected user:password in DSN, got %q", got)
	}
	if !strings.Contains(got, "/meridian_party") {
		t.Errorf("expected database path /meridian_party in DSN, got %q", got)
	}
	if !strings.Contains(got, "simple_protocol") {
		t.Errorf("expected simple_protocol query param in DSN, got %q", got)
	}
}

func TestBuildSuperuserDSN_PreservesCredentials(t *testing.T) {
	tests := []struct {
		name     string
		superDSN string
		dbName   string
		driver   migrations.Driver
		wantUser string
		wantPass string
		wantDB   string
		wantPort string
	}{
		{
			name:     "CockroachDB preserves root credentials",
			superDSN: "postgres://root:secretpass@localhost:26257/defaultdb?sslmode=disable",
			dbName:   "meridian_tenant",
			driver:   migrations.DriverCockroachDB,
			wantUser: "root",
			wantPass: "secretpass",
			wantDB:   "meridian_tenant",
			wantPort: "26257",
		},
		{
			name:     "PostgreSQL preserves postgres credentials",
			superDSN: "postgres://postgres:pgpass@db.example.com:5432/postgres",
			dbName:   "meridian_party",
			driver:   migrations.DriverPostgres,
			wantUser: "postgres",
			wantPass: "pgpass",
			wantDB:   "meridian_party",
			wantPort: "5432",
		},
		{
			name:     "adds default CockroachDB port when missing",
			superDSN: "postgres://root@localhost/defaultdb",
			dbName:   "meridian_platform",
			driver:   migrations.DriverCockroachDB,
			wantUser: "root",
			wantDB:   "meridian_platform",
			wantPort: "26257",
		},
		{
			name:     "adds default PostgreSQL port when missing",
			superDSN: "postgres://postgres:pass@localhost/postgres",
			dbName:   "meridian_current_account",
			driver:   migrations.DriverPostgres,
			wantUser: "postgres",
			wantPass: "pass",
			wantDB:   "meridian_current_account",
			wantPort: "5432",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := migrations.BuildSuperuserDSN(tt.superDSN, tt.dbName, tt.driver)

			parsed, err := url.Parse(result)
			require.NoError(t, err)

			assert.Equal(t, tt.wantUser, parsed.User.Username(), "user mismatch")
			if tt.wantPass != "" {
				pass, _ := parsed.User.Password()
				assert.Equal(t, tt.wantPass, pass, "password mismatch")
			}
			assert.Equal(t, "/"+tt.wantDB, parsed.Path, "database mismatch")
			assert.Equal(t, tt.wantPort, parsed.Port(), "port mismatch")
			assert.Equal(t, "simple_protocol", parsed.Query().Get("default_query_exec_mode"))
		})
	}
}

func TestBuildSuperuserDSN_DiffersFromServiceDSN(t *testing.T) {
	superDSN := "postgres://postgres:secretpass@localhost:5432/postgres"
	sdb := migrations.ServiceDatabase{
		Database: "meridian_party",
		User:     "meridian_party_user",
		Password: "",
	}

	serviceDSN := migrations.BuildServiceDSN(superDSN, sdb, migrations.DriverPostgres)
	superuserTargetDSN := migrations.BuildSuperuserDSN(superDSN, "meridian_party", migrations.DriverPostgres)

	// Service DSN uses service user (passwordless).
	serviceParsed, err := url.Parse(serviceDSN)
	require.NoError(t, err)
	assert.Equal(t, "meridian_party_user", serviceParsed.User.Username())
	_, hasPass := serviceParsed.User.Password()
	assert.False(t, hasPass, "service DSN should not have password")

	// Superuser DSN preserves superuser credentials.
	superParsed, err := url.Parse(superuserTargetDSN)
	require.NoError(t, err)
	assert.Equal(t, "postgres", superParsed.User.Username())
	pass, hasPass := superParsed.User.Password()
	assert.True(t, hasPass, "superuser DSN should preserve password")
	assert.Equal(t, "secretpass", pass)
}
