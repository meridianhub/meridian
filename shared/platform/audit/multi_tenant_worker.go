package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
)

// defaultDiscoveryTimeout bounds each schema-discovery query so a stalled
// database does not block shutdown indefinitely.
const defaultDiscoveryTimeout = 5 * time.Second

// MultiTenantWorker discovers tenant schemas and runs an audit worker for each.
// It periodically scans for new tenant schemas (e.g., when new tenants are provisioned)
// and starts workers for any schemas that have an audit_outbox table.
type MultiTenantWorker struct {
	db            *gorm.DB
	logger        *slog.Logger
	pollInterval  time.Duration
	workerOpts    []WorkerOption
	cancel        context.CancelFunc // cancels the derived context used by all child workers
	shutdown      chan struct{}
	shutdownOnce  sync.Once
	wg            sync.WaitGroup
	mu            sync.Mutex
	activeWorkers map[string]*Worker // schema -> worker
	schemaPattern string             // SQL LIKE pattern for tenant schemas (e.g., "org_%")
}

// NewMultiTenantWorker creates a worker that discovers tenant schemas and processes
// their audit_outbox tables. The schemaPattern is a SQL LIKE pattern used to find
// tenant schemas (e.g., "org_%").
func NewMultiTenantWorker(db *gorm.DB, schemaPattern string, logger *slog.Logger, opts ...WorkerOption) *MultiTenantWorker {
	if logger == nil {
		logger = slog.Default()
	}
	if schemaPattern == "" {
		schemaPattern = "org_%"
	}

	return &MultiTenantWorker{
		db:            db,
		logger:        logger.With("component", "multi-tenant-audit-worker"),
		pollInterval:  30 * time.Second,
		workerOpts:    opts,
		shutdown:      make(chan struct{}),
		activeWorkers: make(map[string]*Worker),
		schemaPattern: schemaPattern,
	}
}

// Start begins the schema discovery loop. The worker derives its own
// cancellable context from ctx so that Stop() can deterministically shut
// down the discovery loop and all child workers.
func (m *MultiTenantWorker) Start(ctx context.Context) {
	childCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	m.wg.Add(1)
	go m.run(childCtx)
	m.logger.Info("multi-tenant audit worker started", "schema_pattern", m.schemaPattern)
}

// Stop shuts down the discovery loop and all active per-schema workers.
func (m *MultiTenantWorker) Stop() {
	m.shutdownOnce.Do(func() {
		m.logger.Info("multi-tenant audit worker stopping")
		close(m.shutdown)
		if m.cancel != nil {
			m.cancel()
		}
	})
	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	for schema, w := range m.activeWorkers {
		w.Stop()
		m.logger.Info("stopped worker for schema", "schema", schema)
	}
	m.logger.Info("multi-tenant audit worker stopped")
}

func (m *MultiTenantWorker) run(ctx context.Context) {
	defer m.wg.Done()

	// Discover schemas immediately on start
	m.discoverAndStartWorkers(ctx)

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.shutdown:
			return
		case <-ticker.C:
			m.discoverAndStartWorkers(ctx)
		}
	}
}

// discoverAndStartWorkers finds tenant schemas with audit_outbox tables
// and starts a worker for any that don't already have one running.
func (m *MultiTenantWorker) discoverAndStartWorkers(ctx context.Context) {
	// Bound the discovery query so a stalled DB does not block shutdown.
	discoverCtx, cancel := context.WithTimeout(ctx, defaultDiscoveryTimeout)
	defer cancel()

	schemas, err := m.findTenantSchemas(discoverCtx)
	if err != nil {
		m.logger.Error("failed to discover tenant schemas", "error", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, schema := range schemas {
		if _, exists := m.activeWorkers[schema]; exists {
			continue
		}

		w := NewAuditWorker(m.db, schema, m.logger, m.workerOpts...)
		w.Start(ctx)
		m.activeWorkers[schema] = w
		m.logger.Info("started audit worker for tenant schema", "schema", schema)
	}
}

// findTenantSchemas queries for schemas matching the pattern that contain an audit_outbox table.
func (m *MultiTenantWorker) findTenantSchemas(ctx context.Context) ([]string, error) {
	var schemas []string
	err := m.db.WithContext(ctx).Raw(`
		SELECT DISTINCT s.schema_name
		FROM information_schema.schemata s
		JOIN information_schema.tables t
		  ON t.table_schema = s.schema_name AND t.table_name = 'audit_outbox'
		WHERE s.schema_name LIKE ?
		ORDER BY s.schema_name
	`, m.schemaPattern).Scan(&schemas).Error
	if err != nil {
		return nil, fmt.Errorf("query tenant schemas: %w", err)
	}
	return schemas, nil
}
