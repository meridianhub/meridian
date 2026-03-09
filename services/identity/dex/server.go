package dex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

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

	// Create in-memory storage.
	memStorage := memory.New(logger)

	// Register the connector definition in storage.
	if err := memStorage.CreateConnector(ctx, storage.Connector{
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
