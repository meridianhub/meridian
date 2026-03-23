package dex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	dexconnector "github.com/dexidp/dex/connector"
	dexserver "github.com/dexidp/dex/server"
	"github.com/dexidp/dex/storage"
	"github.com/dexidp/dex/storage/memory"
)

// EmbeddedDex wraps the Dex storage layer and connector adapter, providing
// all components needed for an in-process Dex OIDC server. The http.Handler
// is set when the Dex server is wired up at the application level.
type EmbeddedDex struct {
	mu      sync.RWMutex
	handler http.Handler
	storage storage.Storage
	adapter *ConnectorAdapter
	logger  *slog.Logger
}

// New creates an EmbeddedDex instance with in-memory storage, registers the
// Meridian connector adapter, and creates OIDC clients. The returned instance
// exposes the storage and adapter for integration with the Dex server.
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

	// Create in-memory storage. dex v2.13.0 uses logrus internally.
	memStorage := memory.New(newDexLogger())

	// Register the connector definition in storage.
	// dex v2.13.0 storage methods do not take a context argument.
	if err := memStorage.CreateConnector(storage.Connector{
		ID:   ConnectorID,
		Type: ConnectorType,
		Name: "Meridian",
	}); err != nil && !errors.Is(err, storage.ErrAlreadyExists) {
		return nil, fmt.Errorf("dex: creating connector in storage: %w", err)
	}

	// Register OIDC clients.
	if err := registerClients(ctx, memStorage, cfg.Clients, logger); err != nil {
		return nil, err
	}

	embedded := &EmbeddedDex{
		storage: memStorage,
		adapter: adapter,
		logger:  logger,
	}

	logger.Info("dex: embedded components initialized",
		"issuer", cfg.Issuer,
		"clients", len(cfg.Clients))

	return embedded, nil
}

// ConnectorID is the storage identifier for the Meridian connector.
const ConnectorID = "meridian"

// ConnectorType is the Dex connector type for the Meridian connector.
const ConnectorType = "meridian"

// SetHandler sets the http.Handler for serving OIDC endpoints. This is called
// by the application layer after creating the Dex server instance.
func (d *EmbeddedDex) SetHandler(h http.Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handler = h
}

// Handler returns the http.Handler that serves Dex's OIDC endpoints.
// Returns nil if SetHandler has not been called.
func (d *EmbeddedDex) Handler() http.Handler {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.handler
}

// Storage returns the underlying Dex storage.
func (d *EmbeddedDex) Storage() storage.Storage {
	return d.storage
}

// Adapter returns the connector adapter that bridges Meridian's authentication
// to Dex's PasswordConnector interface.
func (d *EmbeddedDex) Adapter() *ConnectorAdapter {
	return d.adapter
}

// registerConnectorOnce guards the one-time mutation of dexserver.ConnectorsConfig,
// which is a package-level map that is not safe for concurrent writes.
var registerConnectorOnce sync.Once

// StartServer creates the Dex OIDC HTTP server, registers the Meridian
// connector type, and sets the handler. After this call, Handler() returns
// the Dex server's http.Handler ready for mounting.
func (d *EmbeddedDex) StartServer(ctx context.Context, issuer string, skipApproval bool) error {
	// Register the "meridian" connector type so Dex can resolve it from storage.
	adapter := d.adapter
	registerConnectorOnce.Do(func() {
		dexserver.ConnectorsConfig[ConnectorType] = func() dexserver.ConnectorConfig {
			return &connectorConfigAdapter{adapter: adapter}
		}
	})

	// dex v2.13.0 handles key rotation internally via RotateKeysAfter/IDTokensValidFor.
	srv, err := dexserver.NewServer(ctx, dexserver.Config{
		Issuer:             issuer,
		Storage:            d.storage,
		Logger:             newDexLogger(),
		RotateKeysAfter:    6 * time.Hour,
		IDTokensValidFor:   24 * time.Hour,
		SkipApprovalScreen: skipApproval,
	})
	if err != nil {
		return fmt.Errorf("dex: creating server: %w", err)
	}

	d.SetHandler(srv)
	d.logger.Info("dex: OIDC server started", "issuer", issuer)
	return nil
}

// connectorConfigAdapter implements dexserver.ConnectorConfig, returning the
// pre-created ConnectorAdapter when Dex calls Open during server initialization.
type connectorConfigAdapter struct {
	adapter *ConnectorAdapter
}

func (c *connectorConfigAdapter) Open(_ string, _ logrus.FieldLogger) (dexconnector.Connector, error) {
	return c.adapter, nil
}

// newDexLogger returns a logrus logger for use with the embedded dex library.
// dex v2.13.0 uses logrus internally; this isolates the logrus dependency to this package.
func newDexLogger() logrus.FieldLogger {
	l := logrus.New()
	l.SetLevel(logrus.InfoLevel)
	return l
}
