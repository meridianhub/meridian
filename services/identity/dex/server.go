package dex

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	dexconnector "github.com/dexidp/dex/connector"
	dexserver "github.com/dexidp/dex/server"
	"github.com/dexidp/dex/storage"
	"github.com/dexidp/dex/storage/memory"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// connectorType is the Dex connector type identifier for the Meridian connector.
const connectorType = "meridian"

// EmbeddedDex wraps a Dex OIDC server running in-process. It provides an
// http.Handler that serves the OIDC discovery, authorization, and token
// endpoints.
type EmbeddedDex struct {
	handler http.Handler
	storage storage.Storage
	logger  *slog.Logger
}

// New creates and initializes an embedded Dex server with the given config.
// It sets up in-memory storage, registers the Meridian connector via Dex's
// connector registry, registers OIDC clients, and creates the Dex server.
func New(ctx context.Context, cfg Config) (*EmbeddedDex, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Create the adapter that bridges Meridian's connector to Dex's interface.
	adapter := NewConnectorAdapter(cfg.Connector)

	// Register the "meridian" connector type in Dex's global registry.
	// This allows Dex to instantiate our connector when it loads from storage.
	dexserver.ConnectorsConfig[connectorType] = func() dexserver.ConnectorConfig {
		return &meridianConnectorConfig{adapter: adapter}
	}

	// Create in-memory storage with a logrus logger (required by Dex).
	logrusLogger := newLogrusFromSlog(logger)
	memStorage := memory.New(logrusLogger)

	// Register the connector in storage with static connectors so it's
	// read-only and always available.
	staticConnectors := []storage.Connector{
		{
			ID:   connectorType,
			Type: connectorType,
			Name: "Meridian",
		},
	}
	storageWithConnectors := storage.WithStaticConnectors(memStorage, staticConnectors)

	// Register OIDC clients in the underlying storage (not the static wrapper,
	// which only wraps connector operations).
	if err := registerClients(memStorage, cfg.Clients, logger); err != nil {
		return nil, err
	}

	// Resolve the Dex web assets directory.
	webDir := cfg.WebDir
	if webDir == "" {
		var err error
		webDir, err = findDexWebDir(ctx)
		if err != nil {
			return nil, fmt.Errorf("dex: locating web assets: %w", err)
		}
	}

	// Build and create the Dex server.
	serverConfig := dexserver.Config{
		Issuer:                 cfg.Issuer,
		Storage:                storageWithConnectors,
		SkipApprovalScreen:     cfg.SkipApprovalScreen,
		Logger:                 logrusLogger,
		SupportedResponseTypes: []string{"code"},
		Web: dexserver.WebConfig{
			Dir: webDir,
		},
		PrometheusRegistry: prometheus.NewRegistry(),
	}

	srv, err := dexserver.NewServer(ctx, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("dex: creating server: %w", err)
	}

	embedded := &EmbeddedDex{
		handler: srv,
		storage: memStorage,
		logger:  logger,
	}

	logger.Info("dex: embedded server initialized",
		"issuer", cfg.Issuer,
		"clients", len(cfg.Clients))

	return embedded, nil
}

// Handler returns the http.Handler that serves Dex's OIDC endpoints.
func (d *EmbeddedDex) Handler() http.Handler {
	return d.handler
}

// Storage returns the underlying Dex storage for direct access if needed.
func (d *EmbeddedDex) Storage() storage.Storage {
	return d.storage
}

// meridianConnectorConfig implements dexserver.ConnectorConfig for the Meridian
// connector. It returns the pre-configured adapter when Dex calls Open().
type meridianConnectorConfig struct {
	adapter *ConnectorAdapter
}

// Open returns the Meridian connector adapter. The id and logger parameters
// are provided by Dex but not needed since the adapter is already configured.
func (c *meridianConnectorConfig) Open(_ string, _ logrus.FieldLogger) (dexconnector.Connector, error) {
	return c.adapter, nil
}

// findDexWebDir locates the Dex web assets directory from the Go module cache.
// It uses `go list` to resolve the module's directory path.
func findDexWebDir(ctx context.Context) (string, error) {
	//nolint:gosec // arguments are static, not user-supplied
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", "github.com/dexidp/dex")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolving dex module path: %w", err)
	}
	dir := filepath.Join(strings.TrimSpace(string(out)), "web")
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("dex web directory not found at %s: %w", dir, err)
	}
	return dir, nil
}

// newLogrusFromSlog creates a logrus.FieldLogger for Dex's internal use.
// Dex v2.13 requires logrus; this creates a logrus logger with JSON formatting
// to match Meridian's structured logging style.
func newLogrusFromSlog(_ *slog.Logger) logrus.FieldLogger {
	l := logrus.New()
	l.SetOutput(os.Stdout)
	l.SetFormatter(&logrus.JSONFormatter{})
	l.SetLevel(logrus.InfoLevel)
	return l
}
