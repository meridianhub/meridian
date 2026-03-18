---
name: repository-conventions
description: Canonical repository locations, interface naming, method naming, and implementation patterns per ADR-015
triggers:
  - Adding a repository to a service
  - Naming repository methods
  - Implementing GORM persistence
  - Adding optimistic locking to an entity
  - Where do repository interfaces go?
---

# Repository Conventions

This guide establishes canonical patterns for repository interfaces and their GORM
implementations across Meridian services. It follows the directory structure defined in
[ADR-015](../adr/0015-standard-service-directory-structure.md).

## Directory Layout

```text
services/{service}/
├── domain/
│   └── repository.go       # Interface (port) + sentinel errors
└── adapters/
    └── persistence/
        ├── {entity}.go      # GORM entity struct
        └── repository.go    # GORM implementation (adapter)
```

The domain package defines the **interface** (what the repository can do). The persistence
package provides the **implementation** (how it does it with GORM and CockroachDB).

**Why this separation?** The service layer depends only on the domain interface. Tests can
substitute a fake repository without touching GORM. New adapters (Redis, in-memory) can be
added without changing the domain.

## Interface Location and Naming

Repository interfaces always live in `domain/repository.go` alongside the sentinel errors
they document.

**Interface naming**: `{Entity}Repository` for single-entity repos. When a service manages
multiple entity types, define one interface per aggregate root:

```go
// domain/repository.go
package domain

// SettlementRunRepository defines the contract for persisting and retrieving settlement runs.
type SettlementRunRepository interface {
    // ...
}

// VarianceRepository defines the contract for persisting variances.
type VarianceRepository interface {
    // ...
}
```

The compile-time interface check lives in the implementation file:

```go
// adapters/persistence/repository.go
var _ domain.SettlementRunRepository = (*SettlementRunRepository)(nil)
```

## Method Naming

Meridian uses a consistent verb vocabulary. Choose the right verb based on semantics:

| Operation | Verb | When to use |
|-----------|------|-------------|
| Insert new entity | `Create` | Creating any new aggregate root |
| Insert multiple atomically | `CreateBatch` | Bulk inserts within one transaction |
| Fetch by primary key | `FindByID` | Single entity by UUID/ID |
| Fetch by foreign key | `FindBy{Field}` | One-to-many lookups |
| Fetch multiple | `List` | Paginated/filtered queries |
| Update existing | `Update` | Full aggregate update (uses optimistic locking) |
| Partial field update | `UpdateStatus`, `UpdateFields` | Targeted SQL UPDATE, no version check needed |
| Remove row | `Delete` | Hard delete (rare—prefer status transitions) |
| Soft delete | `Archive` or status transition | Prefer over Delete |
| Upsert | `Upsert` | Only where idempotency requires it |
| Existence check | `Exists` or `IsAvailable` | Boolean queries without loading the entity |

**Real examples from the codebase:**

```go
// services/reconciliation/domain/repository.go
type SettlementRunRepository interface {
    Create(ctx context.Context, run *SettlementRun) error
    FindByID(ctx context.Context, runID uuid.UUID) (*SettlementRun, error)
    Update(ctx context.Context, run *SettlementRun) error
    List(ctx context.Context, filter RunFilter) ([]*SettlementRun, error)
}

// services/position-keeping/domain/repository.go
type FinancialPositionLogRepository interface {
    Create(ctx context.Context, log *FinancialPositionLog) error
    CreateBatch(ctx context.Context, logs []*FinancialPositionLog) error
    FindByID(ctx context.Context, logID uuid.UUID) (*FinancialPositionLog, error)
    FindByAccountID(ctx context.Context, accountID string, filter LogFilter) ([]*FinancialPositionLog, error)
    Update(ctx context.Context, log *FinancialPositionLog) error
}

// services/tenant/domain/repository.go
// Note: tenant uses UpdateStatus (targeted) rather than a full Update method.
// Domain entities with multiple write paths may use targeted update methods
// rather than a single generic Update.
type TenantRepository interface {
    Create(ctx context.Context, tenant *Tenant) error
    GetByID(ctx context.Context, id tenant.TenantID) (*Tenant, error)
    GetBySlug(ctx context.Context, slug string) (*Tenant, error)
    IsSlugAvailable(ctx context.Context, slug string) (bool, error)
    UpdateStatus(ctx context.Context, id tenant.TenantID, status Status, currentVersion int) (*Tenant, error)
    UpdateStatusWithError(ctx context.Context, id tenant.TenantID, status Status, errorMsg string, currentVersion int) (*Tenant, error)
    List(ctx context.Context, statusFilter *Status, pageSize int, pageToken string) ([]*Tenant, string, error)
}
```

> Note: `GetByID` vs `FindByID` — both exist in the codebase. Prefer `FindByID` for new
> services (consistent with the majority pattern in domain repositories). `GetByID` is
> acceptable but avoid mixing both in the same interface.

## Error Documentation Convention

Document each method's error contract in the interface comment. This makes callers aware
of sentinel errors without reading the implementation:

```go
// Create persists a new SettlementRun.
// Returns ErrConflict if a run with the same RunID already exists.
Create(ctx context.Context, run *SettlementRun) error

// FindByID retrieves a SettlementRun by its RunID.
// Returns ErrNotFound if the run doesn't exist.
FindByID(ctx context.Context, runID uuid.UUID) (*SettlementRun, error)

// Update updates an existing SettlementRun using optimistic locking.
// Returns ErrNotFound if the run doesn't exist.
// Returns ErrOptimisticLock if the version doesn't match.
Update(ctx context.Context, run *SettlementRun) error
```

> **ErrOptimisticLock vs ErrVersionConflict**: The domain layer defines `ErrOptimisticLock`
> as the semantic error for concurrent modification. The persistence layer defines
> `ErrVersionConflict` (entity-prefixed in practice, e.g., no prefix needed when there is
> only one entity type per repo file). Both name the same condition at different layers.
> Service code checks `persistence.ErrVersionConflict`; domain interface comments reference
> `ErrOptimisticLock`. This is intentional layering—domain errors describe business
> semantics, persistence errors describe storage outcomes.

## GORM Entity Structure

GORM entities live in `adapters/persistence/` alongside the repository. Use flat structs
(no nesting) and explicit GORM tags.

### Standard Fields

Every entity that is an aggregate root includes:

```go
type SettlementRunEntity struct {
    ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
    RunID     uuid.UUID      `gorm:"column:run_id;uniqueIndex;type:uuid;not null"` // business key
    CreatedAt time.Time      `gorm:"not null;default:now()"`
    UpdatedAt time.Time      `gorm:"not null;default:now()"`
    DeletedAt gorm.DeletedAt `gorm:"index"`              // soft delete (optional)
    Version   int64          `gorm:"not null;default:1"` // optimistic locking
    // ... domain fields
}
```

> **DB PK vs business key**: `ID` is the CockroachDB-managed row primary key
> (`gen_random_uuid()`). `RunID` is the domain-visible identifier, uniquely indexed and
> used in all WHERE clauses. The domain layer never exposes `ID` directly.

### TableName

Every entity **must** implement `TableName()` returning a singular, unqualified name:

```go
// TableName returns the singular, unqualified table name.
// Unqualified (no schema prefix) allows search_path routing for tenant isolation.
func (SettlementRunEntity) TableName() string {
    return "settlement_run"  // singular, no schema prefix
}
```

**Why singular?** Natural SQL syntax: `FROM settlement_run WHERE ...`

**Why unqualified?** CockroachDB's `search_path` routes queries to the correct tenant
schema. Adding a schema prefix bypasses this routing.

## Optimistic Locking Pattern

All mutable aggregate roots use optimistic locking. The domain entity carries a `Version`
field, incremented on every write. The repository checks that the DB version matches before
updating:

```go
// In the domain entity:
type SettlementRun struct {
    id      uuid.UUID
    version int64
    // ...
}

func (r *SettlementRun) Version() int64 { return r.version }

// In the GORM repository Update method:
func (r *SettlementRunRepository) Update(ctx context.Context, run *domain.SettlementRun) error {
    // The entity's Version was already incremented by the domain before calling Update.
    // Check that the DB still has the *previous* version.
    expectedDBVersion := run.Version() - 1

    result := tx.Model(&SettlementRunEntity{}).
        Where("run_id = ? AND version = ?", run.ID(), expectedDBVersion).
        Updates(map[string]interface{}{
            "status":     string(run.Status()),
            "version":    run.Version(),
            "updated_at": time.Now(),
        })

    if result.Error != nil {
        return result.Error
    }
    if result.RowsAffected == 0 {
        // Either the entity was deleted (ErrNotFound) or concurrently modified (ErrVersionConflict).
        // Distinguish by doing a follow-up existence check, or return ErrVersionConflict by default.
        return ErrVersionConflict
    }
    return nil
}
```

The service layer maps `persistence.ErrVersionConflict` to `codes.Aborted`, signaling
the caller to reload and retry.

## Tenant Scoping

All queries must run within a tenant-scoped transaction. Use `db.WithGormTenantScope`:

```go
func (r *VarianceRepository) withTenantTransaction(
    ctx context.Context,
    fn func(tx *gorm.DB) error,
) error {
    tx, err := db.WithGormTenantScope(ctx, r.db.WithContext(ctx))
    if err != nil {
        return err
    }
    return tx.Transaction(fn)
}
```

Never use `r.db` directly for tenant-scoped data. Always pass `ctx` through so the
tenant interceptor can set `search_path` on the connection.

## Pagination

Use `Limit` + `Offset` for simple pagination, or cursor-based pagination when the result
set is large or ordering by created_at is needed.

### Offset Pagination

```go
type Filter struct {
    Status *RunStatus
    Limit  int
    Offset int
}

// In the query:
query = query.Limit(filter.Limit).Offset(filter.Offset)
```

### Cursor Pagination

Used in `services/party` for large party lists. The cursor encodes `created_at + id` to
handle ties:

```go
type PartyCursor struct {
    CreatedAt time.Time
    ID        uuid.UUID
}

// Encode cursor to opaque page token
func EncodePartyCursor(c PartyCursor) string {
    data := c.CreatedAt.Format(time.RFC3339Nano) + "|" + c.ID.String()
    return base64.URLEncoding.EncodeToString([]byte(data))
}
```

Use cursor pagination when:
- The dataset is large (thousands of rows)
- Stable ordering is required under concurrent inserts
- The caller needs to resume from a previous position

## Constructor and Compile-Time Check

Always add the compile-time interface assertion in the implementation file:

```go
package persistence

// Compile-time check that VarianceRepository implements domain.VarianceRepository.
var _ domain.VarianceRepository = (*VarianceRepository)(nil)

type VarianceRepository struct {
    db *gorm.DB
}

func NewVarianceRepository(db *gorm.DB) *VarianceRepository {
    return &VarianceRepository{db: db}
}
```

This catches interface drift at compile time rather than at runtime.

## Integration Tests

Every repository implementation requires integration tests using CockroachDB Testcontainers:

```go
func TestVarianceRepository(t *testing.T) {
    db, cleanup := testdb.SetupCockroachDB(t, []interface{}{&VarianceEntity{}})
    defer cleanup()

    repo := NewVarianceRepository(db)

    t.Run("Create and FindByID", func(t *testing.T) {
        // happy path
    })

    t.Run("FindByID returns ErrNotFound for missing entity", func(t *testing.T) {
        _, err := repo.FindByID(ctx, uuid.New())
        assert.ErrorIs(t, err, domain.ErrNotFound)
    })

    t.Run("Update returns ErrVersionConflict on concurrent modification", func(t *testing.T) {
        // create entity, simulate concurrent write, verify conflict
        assert.ErrorIs(t, err, ErrVersionConflict)
    })
}
```

See [testcontainers-usage.md](testcontainers-usage.md) for full setup details.

## Reference Implementations

| Service | Best Reference For |
|---------|--------------------|
| `services/party/adapters/persistence/` | Cursor pagination, multiple entity types |
| `services/reconciliation/adapters/persistence/` | Multiple repositories in one service |
| `services/position-keeping/adapters/persistence/` | Batch inserts, complex queries |
| `services/tenant/adapters/persistence/` | Soft delete, slug indexing |
