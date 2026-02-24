package migrations_test

import (
	"strings"
	"testing"

	"github.com/meridianhub/meridian/internal/migrations"
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
