# Meridian Project - Claude Code Instructions

## AI Navigability Docs

For AI contributors and new engineers, these documents describe the codebase structure:

- [docs/architecture-layers.md](docs/architecture-layers.md) - 8-layer functional grouping with service-to-layer mapping
- [docs/patterns.md](docs/patterns.md) - 6 cross-service patterns with canonical locations
- [docs/data-flows.md](docs/data-flows.md) - 4 sequence diagrams: payment, audit, tenant provisioning, manifest apply
- [docs/saga-handler-loading.md](docs/saga-handler-loading.md) - Starlark saga runtime loading flow
- [docs/service-readme-template.md](docs/service-readme-template.md) - required structure for per-service READMEs
- [cookbook/README.md](cookbook/README.md) - pattern templates vs reference-data distinction

Every service has its own `README.md` following the template. When a service-level question
comes up, read that service's README first.

## Skills

For all available skills, ADRs, runbooks, and PRDs, see `.claude/skills/README.md`.

---

## Task Master Tag Safety

**CRITICAL**: Task Master uses GLOBAL tag state. When setting task status, ALWAYS ensure you're operating on the correct tag.

**Safe patterns:**

```bash
# Chain tag switch with command (preferred)
task-master tags use <tag-name> && task-master set-status --id=<id> --status=<status>

# Or use --tag parameter where supported
task-master list --tag <tag-name>
task-master next --tag <tag-name>
```

**NEVER do this:**

```bash
# WRONG - tag can change between commands in multi-terminal environments
task-master tags use <tag-name>
task-master set-status --id=1 --status=done  # Might execute on wrong tag!
```

**NEVER run `task-master add-task` as parallel background jobs.** Each `add-task` call internally switches the global tag, and concurrent invocations race with each other — tasks silently land on wrong tags. Always run `add-task` sequentially (inline, not background). This also applies to `set-status`, `update-task`, and any command that modifies task state.

**Why this matters:** Multiple terminal sessions share the same global tag state. If you switch tags in one terminal then run a command in another, you may modify the wrong tag's tasks.

---

## Testing Guidelines

### Use `await` Instead of `time.Sleep`

**NEVER use `time.Sleep` in tests.** Use the `shared/platform/await` package instead.

`time.Sleep` creates flaky tests - sleeping too short causes failures, sleeping too long wastes CI time. The `await` package polls conditions until they're met or timeout, making tests both reliable and fast.

```go
import "github.com/meridianhub/meridian/shared/platform/await"

// BAD - arbitrary sleep, flaky and slow
time.Sleep(2 * time.Second)
assert.Equal(t, "COMPLETED", order.Status)

// GOOD - polls until condition met or timeout
err := await.Until(func() bool {
    return order.Status == "COMPLETED"
})
require.NoError(t, err)

// With custom timeout and poll interval
err := await.New().
    AtMost(5 * time.Second).
    PollInterval(50 * time.Millisecond).
    Until(func() bool {
        return repo.FindByID(ctx, id) != nil
    })

// Wait for an operation to succeed
err := await.UntilNoError(func() error {
    return client.HealthCheck()
})
```

Defaults: 10s timeout, 100ms poll interval. For advanced matchers, consider `gomega.Eventually()`.

---

## Temporal Quality Ladder

Confidence grades are `ESTIMATE -> PROVISIONAL -> ACTUAL -> VERIFIED` (Axis A).

- **`COEFFICIENT` is a data source, not a level.** Profile-coefficient calculation
  (`ESTIMATED_PROFILE`) is recorded in the Source Authority Registry and maps to the
  `ESTIMATE` grade. It is a provenance attribute, not a rung on the ladder.
- **`REVISED` is a lifecycle event, not a confidence grade.** It is removed from the
  Axis A enum; corrections are expressed on Axis B via the `revision` counter,
  the `SupersededBy` pointer, and bitemporal validity. Proto slot 4, formerly
  `REVISED`, is now `VERIFIED`.

See [docs/adr/0017-temporal-quality-ladder.md](docs/adr/0017-temporal-quality-ladder.md).

---

## Database: CockroachDB

**Meridian uses CockroachDB as its production database**, not PostgreSQL. While CockroachDB is PostgreSQL wire-compatible, there are important differences that affect code design:

### Key CockroachDB Limitations

| Feature | PostgreSQL | CockroachDB | Workaround |
|---------|------------|-------------|------------|
| LISTEN/NOTIFY | Supported | **Not supported** | Use polling or outbox pattern |
| PL/pgSQL triggers | Full support | **Not supported in UDFs** | Enforce lifecycle logic at Go application layer |
| ALTER COLUMN TYPE in transactions | Supported | **Not supported** | Run schema changes outside transactions |
| Range types (TSTZRANGE) | Supported | **Not supported** | Use separate start/end columns |
| Partial index on new column | Same-txn OK | **Column must be "public" first** | Split into separate migration file |
| DML on new column | Same-txn OK | **Column must be "public" first** | Split INSERT/UPDATE into separate migration file |
| `COMMENT ON INDEX` | `index_name` | **`table@index_name`** | Omit COMMENT ON INDEX (use SQL comments instead) |
| Expression indexes (`date_trunc()`) | Supported | **Context-dependent ops not allowed** | Use plain column indexes |
| `CREATE INDEX CONCURRENTLY` | Async, non-blocking | **Redundant** (all DDL is online) | Omit CONCURRENTLY |

### Migrations: Atlas (NOT Flyway)

**Meridian uses [Atlas](https://atlasgo.io/) for database migrations**, not Flyway. Each service has its own migration directory and Atlas config:

```
services/<service>/migrations/    # Migration SQL files + atlas.sum
services/<service>/atlas/atlas.hcl # Atlas config (env: local, ci, production)
```

**Naming convention**: `YYYYMMDD000NNN_description.sql` (e.g., `20260210000001_reference_data_node.sql`)

**Key commands**:

```bash
# After adding/modifying migration files, update the hash
atlas migrate hash --dir file://services/<service>/migrations

# Validate migrations
atlas migrate validate --dir file://services/<service>/migrations --dev-url "docker://postgres/16/dev"

# Generate a new migration from GORM model diff (from repo root)
atlas migrate diff <description> --env local --config file://services/<service>/atlas/atlas.hcl
```

**Rules**:

- Always update `atlas.sum` after adding migration files (`atlas migrate hash`)
- Migration files use `-- atlas:txn false` directive when DDL cannot run inside a transaction (e.g., `CREATE INDEX CONCURRENTLY` equivalent scenarios)
- Atlas source of truth is GORM models loaded via `utilities/atlas-loader`

### Migration Rules for CockroachDB

**CRITICAL**: These patterns cause failures on CockroachDB. Follow these rules in all migration files:

1. **Never create a partial index on a column added in the same migration.** CockroachDB requires the column to be committed ("public") before a partial index can reference it. Split into two files:

   ```sql
   -- 20260101000001_add_column.sql
   ALTER TABLE foo ADD COLUMN bar VARCHAR(255) NULL;

   -- 20260101000002_add_index.sql  (separate migration)
   CREATE INDEX idx_foo_bar ON foo (bar) WHERE bar IS NOT NULL;
   ```

2. **Never reference a newly-added column in DML within the same migration.** UPDATE/INSERT using a column added by ALTER TABLE in the same transaction will fail. Split the DML into a subsequent migration.

3. **Never use PL/pgSQL triggers.** CockroachDB does not support `LANGUAGE plpgsql` in user-defined functions. All lifecycle enforcement (status transitions, immutable fields, timestamp management) must be at the Go application layer.

4. **Never use `COMMENT ON INDEX index_name`.** CockroachDB requires `table@index_name` syntax. Use SQL comments (`--`) instead.

5. **Never use expression indexes with context-dependent functions.** `date_trunc()`, `NOW()`, etc. cannot appear in expression indexes. Use plain column indexes.

6. **Omit `CONCURRENTLY` from `CREATE INDEX`.** CockroachDB creates all indexes online by default. The keyword is unnecessary and can cause timing issues with `atlas:txn false`.

### Testing with CockroachDB

Always use CockroachDB testcontainers for integration tests to ensure production parity:

```go
import "github.com/meridianhub/meridian/shared/platform/testdb"

func TestMyFeature(t *testing.T) {
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    // Your test code...
}
```

The `setupTestPostgres` helper in test files wraps `testdb.SetupCockroachDB` for historical compatibility.

### Event-Driven Patterns

Since CockroachDB doesn't support LISTEN/NOTIFY, use these alternatives:

1. **Polling**: For orphan detection, lease expiry, etc. - periodic scans with configurable intervals
2. **Outbox Pattern**: For reliable event delivery - write events to an outbox table, background worker publishes to Kafka

See `shared/platform/events/outbox.go` for the outbox pattern implementation.

---

## Marathon Configuration

Project-specific settings for `/tm` marathon mode. The generic `/tm` template reads these.

### Branch and Merge

- **Base branch**: `develop`
- **PR target branch**: `develop`
- **Required approvals**: 2 (minimum for auto-merge)
- **Markdown-only PR approvals**: 1 (bot reviewers skip markdown PRs)

### Bot Reviewers

**CodeRabbit** (`coderabbitai[bot]`):

- Fix code and push. CodeRabbit re-reviews automatically and resolves its own threads.
- **NEVER reply in CodeRabbit threads** - CodeRabbit ignores replies from other bots, and replies pollute the thread.
- `request_changes_workflow` is enabled: CodeRabbit submits CHANGES_REQUESTED reviews. When it re-reviews and approves, GitHub does NOT dismiss the old CR. This is a GitHub limitation. Every PR needs stale bot CR dismissal before merging - don't investigate, just dismiss.

**claude[bot]** (`claude[bot]`):

- Resolve threads via GraphQL after addressing the feedback.

**Human reviewers**:

- Fix code, reply inline, @mention reviewer. Do NOT resolve human threads - let them confirm.

### CI Patterns

- **Go test shards**: Frequently slow/queued (10+ min). NFR benchmarks are flaky on shared runners. Re-run failed shards rather than investigating.
- **Known flaky tests**: `TestInstructionRepository_FetchDispatchable_RespectsNextRetryAt` (operational-gateway), `TestInstructionRepository_FetchDispatchable_SkipsAlreadyDispatching` (operational-gateway). Pre-existing, safe to merge past.
- **Trivy Repository Scan**: Pre-existing CVE failures in dependencies. Non-blocking.
- **CockroachDB testcontainers**: Shard runtime varies significantly. Not actionable.
- **E2E shards**: Frontend Playwright tests. Backend-only PRs with E2E failures are safe to merge.
- **codecov/patch**: Informational, not a merge gate.

### Retrospective

- **Retro log**: `marathon-retros.md` in your local Task Master project memory directory
- Append each marathon's retrospective to this log after completion
- Update the Template Changes validation column for any "Pending" items that were exercised
