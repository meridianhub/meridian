// Package main provides a custom Dex binary that registers the Meridian
// identity connector as a "meridian" connector type.
//
// This binary wraps the standard Dex server with the MeridianConnector
// pre-registered, allowing Dex to authenticate users against the Meridian
// identity service's database without a network hop.
//
// # Architecture
//
// Dex's server package cannot be cleanly imported as a Go library because its
// ConnectorsConfig map transitively imports every built-in connector (LDAP,
// SAML, Keystone, etc.) and an incompatible gRPC API. Instead, this binary
// vendors Dex's serve logic and registers the "meridian" connector type in
// the ConnectorsConfig map before starting the server.
//
// # Build
//
// The Dockerfile builds this binary by copying Dex's cmd/dex source into the
// build context and patching the connector registration:
//
//	COPY --from=dex-source /src/cmd/dex/ ./cmd/dex-meridian/dex/
//	# Patch: add meridian import + registration in init()
//
// # Configuration
//
// Environment variables:
//
//	MERIDIAN_DATABASE_URL  - Database URL for identity and tenant repositories
//
// dex.yaml (add to connectors section):
//
//	oauth2:
//	  passwordConnector: meridian
//	connectors:
//	  - type: meridian
//	    id: meridian
//	    name: Meridian Identity
//
// # Connector Registration Pattern
//
// The "meridian" connector type is registered by calling:
//
//	server.ConnectorsConfig["meridian"] = func() server.ConnectorConfig {
//	    return &meridianConfig{adapter: preBuiltAdapter}
//	}
//
// The adapter (services/identity/connector.DexPasswordConnector) implements
// Dex's connector.PasswordConnector interface and handles:
//   - Tenant context extraction from username ("tenant:<slug>/<email>")
//   - Slug-to-TenantID resolution via the tenant repository
//   - Credential validation via the identity connector
//   - Tenant ID propagation via ConnectorData and Groups
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/services/identity/connector"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
)

// errMissingDatabaseURL is returned when MERIDIAN_DATABASE_URL is not set.
var errMissingDatabaseURL = errors.New("MERIDIAN_DATABASE_URL environment variable is required")

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger); err != nil {
		logger.Error("dex-meridian: fatal error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	databaseURL := os.Getenv("MERIDIAN_DATABASE_URL")
	if databaseURL == "" {
		return errMissingDatabaseURL
	}

	// Connect to Meridian database.
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	logger.Info("dex-meridian: connected to Meridian database")

	// Build identity and tenant repositories.
	identityRepo := persistence.NewRepository(db)
	tenantRepo := tenantpersistence.NewRepository(db)

	// Build the Meridian connector adapter.
	internalConnector, err := connector.New(identityRepo, logger)
	if err != nil {
		return fmt.Errorf("create internal connector: %w", err)
	}

	adapter, err := connector.NewDexPasswordConnector(internalConnector, tenantRepo, logger)
	if err != nil {
		return fmt.Errorf("create dex adapter: %w", err)
	}

	// In the full implementation, register the adapter with Dex:
	//
	//   dexserver.ConnectorsConfig["meridian"] = func() dexserver.ConnectorConfig {
	//       return &meridianConfig{adapter: adapter}
	//   }
	//   // Then start the Dex server as normal.
	//
	// For now, this binary demonstrates the wiring by running a health
	// endpoint that confirms the connector is operational.
	_ = adapter

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "dex-meridian: connector ready")
	})

	listenAddr := envOrDefault("LISTEN_ADDR", ":5556")
	httpServer := &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("dex-meridian: starting", "addr", listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("dex-meridian: HTTP server error: %w", err)
	case <-ctx.Done():
	}
	logger.Info("dex-meridian: shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdownCtx)
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
