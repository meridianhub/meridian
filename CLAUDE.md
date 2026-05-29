# Meridian Project - Claude Code Instructions

## Mission: The Operating System for the Real-World Economy

> "Meridian is what you get if 10x Banking and Murex had a baby, and built it with the operational physics of Kraken."

We are building the first **Open-Source Transaction Integrity Engine** designed to manage the convergence of Finance, Energy, and Infrastructure.

### The Three DNA Strands

**1. The "10x Banking" DNA (The Core)**

- Real-time ledgers, BIAN compliance, strict double-entry accounting, immutable audit trails
- Saga patterns guarantee mathematical correctness
- *Because whether you're moving money or megawatts, you cannot lose data*

**2. The "Murex" DNA (The Brain)**

- Multi-asset architecture at the core: Energy (kWh), Compute (GPU-Hours), Carbon Credits treated exactly like financial instruments
- *Modern companies hold inventory, energy contracts, and digital rights - not just cash*

**3. The "Kraken" DNA (The Muscle)**

- Time-Bound Quality Ladder and High-Frequency Buffer for massive streams of physical data
- Handle "Estimates vs. Actuals" reconciliation without locking the database
- *Banks are too slow for the energy grid. Meridian operates at the speed of infrastructure.*

### The Market Gap

| Player | Limitation |
|--------|------------|
| Murex | $10M+/year, 3-year installs, built for Trading Floors not Operations |
| 10x / Thought Machine | Brilliant banks, but Fiat Only - cannot track kWh or vouchers natively |
| Kraken | Proprietary and Closed - unavailable unless you're a massive utility |
| **Meridian** | **Open Source & Universal** - accessible to NGOs, AI startups, and energy co-ops today |

### The Vision: Kubernetes for Economies

Meridian is **Kubernetes for economies**. The manifest declares the desired economic model. The platform continuously operates it.

Terraform provisions infrastructure then gets out of the way. Meridian provisions *and runs*. The sagas are runtime, not setup. Scheduled billing fires every month. Settlement triggers when market data arrives. Compensation reverses failed steps automatically. The quality ladder reconciles estimates against actuals as better data flows in. The manifest *is* the running system.

**The proof:** A complete tote betting platform - instruments, accounts, double-entry settlement, Stripe integration, event-driven payouts - was designed in a single conversation and expressed in under 400 lines of YAML + Starlark. No code deployment. No PRs to Meridian core. No migrations. Just a manifest that declares "here's how tote betting works" and the engine runs it.

That's not configuration. That's a new economy, defined declaratively, operated continuously.

We are entering the era of **Real World Assets (RWA)**:

- The UN needs to track digital vouchers (Murex logic) on flaky networks (Kraken logic)
- AI Clouds need to bill for GPU milliseconds (Kraken logic) with financial rigor (10x logic)
- A betting syndicate needs to pool stakes, settle on match results, and distribute winnings with the same financial rigour as a bank transfer

**Meridian is the Infrastructure Layer that powers this new economy. We provide the Physics of Value so builders can focus on the product.**

---

## The Meta-Innovation: Infrastructure That AI Can Safely Configure

Meridian isn't just a ledger - it's the first **AI-Configurable Transaction Engine**.

### The Problem With Traditional Platforms

Every ledger platform faces the **Flexibility vs Safety Tradeoff**:

- **Too rigid** (hard-coded): Safe but can't adapt to new asset classes
- **Too flexible** (JSON configs, magic strings): Adaptable but error-prone at runtime

Stripe, Modern Treasury, even Thought Machine - they all force you to write integration code that can fail in production.

### Meridian's Solution: Compile-Time Safety in Dynamic Scripts

We solved this with three architectural innovations:

**1. Schema-Driven Service Modules**

```yaml
# handlers.yaml - Single source of truth
position_keeping.initiate_log:
  params:
    amount:
      type: Decimal
      required: true
    direction:
      type: enum
      values: [DEBIT, CREDIT]
```

**2. Auto-Generated Starlark Clients**

```python
# Type-safe! Caught at script load, not runtime
position_keeping.initiate_log(
    amount=Decimal("100.00"),  # Type-checked
    direction="CREDIT",         # Enum-validated
)
```

**3. AI-Assisted Configuration**

```
AI: "What does your business do?"
→ Generates handlers.yaml schema
→ BuildServiceModules() creates type-safe clients
→ AI generates Starlark sagas using those clients
→ Tenant deploys, zero compilation needed
```

**Why This Matters:**

- **Banks** can't build this (too conservative, vendor-locked)
- **Fintechs** can't build this (focus on UX, not infrastructure)
- **Big Tech** won't build this (not their market)
- **We** can build this (open source + AI-native + financial rigor)

The result: **Murex-grade infrastructure at Stripe-level simplicity**, configurable by conversation instead of 6-month implementations. Not just provisioned - *continuously operated*. The manifest doesn't describe what to build. It describes what to *run*.

---

## Why This Is Cognitively Fascinating (For Claude Code)

### 1. **Bounded Expressiveness: The Goldilocks Zone of Programmability**

How do you give tenants powerful programmability WITHOUT letting them:

- Write infinite loops that DoS the platform?
- Create non-deterministic execution times?
- Consume unbounded memory?

**Answer:** Choose languages that are **intentionally NOT Turing-complete**.

**Starlark:**

- ✅ No `while` loops (only `for` loops over finite iterables)
- ✅ No recursion
- ✅ All programs guaranteed to terminate
- ✅ Deterministic execution time (no runaway computation)
- ✅ Still expressive enough for complex business logic

**CEL (Common Expression Language):**

- ✅ Expression evaluation only (no loops at all)
- ✅ Used for validation rules: `amount > 0 && amount <= 1000000`
- ✅ Sub-millisecond execution guaranteed
- ✅ Cannot modify state (pure functions only)

**Why This Matters:**

Traditional platforms face the "Halting Problem":

- Give users Python/JavaScript → They can write `while True: pass` and crash your platform
- Restrict to JSON configs → Too limited for real business logic
- Build custom DSL → Takes years, brittle, hard to maintain

**Meridian's solution:** Use battle-tested languages (Starlark from Google/Bazel, CEL from Google/Kubernetes) that:

- Are proven to scale (Google uses them for billions of operations)
- Guarantee termination (mathematical proof, not runtime monitoring)
- Are just Python syntax - no new language to learn, your team already reviews it
- AI can generate them reliably, constrained by the schema registry so hallucinations hit a type-checker wall, not production
- Produce declarative manifests that the UI can render as a visual graph - you can *see* your economy before anything runs

**The Cognitive Challenge:**

> "Build a ledger where tenants define arbitrarily complex workflows, but you can mathematically guarantee:
>
> - Every saga completes in bounded time - predictable latency, real SLAs
> - Every validation runs in < 10ms - no user-facing lag on any operation
> - Secure by construction - the language can't express dangerous operations, the attack surface doesn't exist
> - All execution is deterministic - debug once, trust everywhere"

**Traditional answer:** "Impossible - you need Turing-completeness for real workflows."

**Meridian's answer:** "No, you don't. Most business logic is:

- Finite state machines (Starlark sagas)
- Conditional validation (CEL expressions)
- Deterministic transformations (CEL mappings)

Turing-completeness is a LIABILITY, not a feature."

#### The Starlark Design Constraint

```python
# ✅ ALLOWED - Finite loop over known collection
for customer in customers:
    if customer.balance > 0:
        send_invoice(customer)

# ❌ FORBIDDEN - While loop could run forever
while True:
    process_next_item()  # Starlark compiler rejects this

# ❌ FORBIDDEN - Recursion could blow stack
def factorial(n):
    return n * factorial(n - 1)  # Starlark compiler rejects this

# ✅ ALLOWED - Tail recursion via iteration
def factorial(n):
    result = 1
    for i in range(1, n + 1):
        result *= i
    return result
```

**Why This Design Choice is Brilliant:**

| Tenant Concern | Turing-Complete (Python) | Bounded (Starlark) |
|---------|-------------------------|-------------------|
| **Security** | Runtime exploits possible | ❌ Compiler rejects dangerous patterns |
| **Reliability** | Sagas can hang forever | ✅ Guaranteed completion, real SLAs |
| **Correctness** | Race conditions, threads | ✅ Single-threaded, deterministic |
| **Reviewability** | Custom DSL to learn | ✅ It's just Python - your team already reads it |
| **AI generation** | Hallucinations hit production | ✅ Schema-constrained, compile-time checked |
| **Visualization** | Opaque runtime behavior | ✅ Declarative manifest renders as a graph |

**This is the same choice Google made:**

- **Bazel** (build system): Starlark for build configs (must terminate)
- **Kubernetes**: CEL for admission control (must be fast)
- **Firebase**: CEL for security rules (must be safe)

Meridian applies this to **financial operations** - where non-termination doesn't just annoy users, it **loses money**.

#### The CEL Complement

CEL handles the "hot path" - fast decisions that must complete in < 10ms:

```python
# Asset validation (CEL expression stored in DB)
validation_rule = "amount > 0 && amount <= account.credit_limit"

# Evaluation
result = cel_engine.eval(validation_rule, {
    "amount": 1000.00,
    "account": {"credit_limit": 5000.00}
})
# ✅ Returns: true (in < 1ms)
```

**Why CEL + Starlark Together:**

| Use Case | Language | Why |
|----------|----------|-----|
| Validation rules | CEL | < 1ms execution, no loops needed |
| Pricing formulas | CEL | Pure math, deterministic |
| Saga workflows | Starlark | Needs conditionals + loops, but must terminate |
| Compensation logic | Starlark | Reverse operations (LIFO), finite steps |

**The Result:**

Tenants get **98% of the expressiveness** of Python, with **100% guarantee** of termination. It's Python they already know how to read and write, minus the two features (while loops, recursion) that would make their sagas unprovable. The constraints don't limit what tenants can build - they make what tenants build *trustworthy*: secure by construction, compile-time checked, AI-generatable, human-reviewable, and visualizable in the UI as a live graph of their economy.

#### Why This is Harder Than Turing-Completeness

It's EASY to give tenants Python and say "good luck, don't crash the platform."

It's HARD to:

1. Identify the **minimal expressive power** needed for real workflows
2. Choose languages that **provably terminate**
3. Make it feel like Python (because it *is* Python) so there's nothing new to learn
4. Build the constraint chain: schema → compile-time checks → AI generation → human review → visual graph

**Meridian did the hard thing.**

Banks can't do this (they use Turing-complete languages with runtime monitors - fragile).
Fintechs can't do this (they use JSON configs - too limited).
Big Tech won't do this (not their problem space).

**This is original research** applied to production finance systems.

### 2. **Temporal Data Quality as First-Class Type**

Most systems treat data as binary (present/absent). Meridian treats data as having **provenance**:

```
Quality Ladder: ESTIMATE → COEFFICIENT → ACTUAL → REVISED
                   ↓           ↓           ↓         ↓
             (forecast)  (modeled)    (metered)  (corrected)
```

Every measurement carries:

- What we know (the value)
- How we know it (the quality)
- When we knew it (bi-temporal validity)

This isn't just "nice to have" - it's **required** for:

- Energy settlement (meter reads come late, out of order)
- Carbon accounting (credits verified months after purchase)
- Financial reconciliation (trades settle T+2, T+3)

**The challenge:** Build a ledger where the same transaction has MULTIPLE valid states depending on when you query it.

### 3. **Multi-Asset Dimensional Types**

Traditional ledgers: `Amount` is a number.
Meridian: `Quantity[D]` is a **dimensioned value** where `D` can be:

```go
type Quantity[D Dimension] struct {
    Amount    decimal.Decimal
    Unit      InstrumentCode  // "GBP", "kWh", "TONNE_CO2E", "GPU_HOUR"
}

// Compile-time safety
var deposit Quantity[Currency]      // ✅ Can deposit GBP
var energy Quantity[Electricity]    // ✅ Can transfer kWh
deposit = energy                    // ❌ Compiler error - can't mix dimensions
```

This is physics-style dimensional analysis applied to finance. You literally cannot accidentally add kilowatt-hours to British pounds.

**Why it's hard:** Most languages don't support phantom types well enough for this. Go generics (1.18+) barely make it possible.

### 4. **Saga Orchestration with Automatic Compensation**

Every workflow is a **distributed transaction** across microservices:

```python
# Step 1: Reserve credit
lien_result = create_lien(...)

# Step 2: Charge customer
payment_result = charge_card(...)

# If step 2 fails, step 1 AUTOMATICALLY compensates
# Compensation: release_lien (defined in schema)
```

**The challenge:** Guarantee EXACTLY-ONCE semantics in a world where:

- Networks fail mid-request
- Services restart during operations
- Idempotency keys can collide
- Compensation can fail too

We use **event sourcing + saga state machines** to make this mathematically correct, even under chaos.

### 5. **The AI Code Generation Problem**

LLMs hallucinate. They invent APIs that don't exist, use wrong parameter names, mix up types.

**Meridian's constraint system makes AI reliable:**

1. **Schema Registry** = Source of truth (handlers.yaml)
2. **Service Modules** = Auto-generated from schema
3. **AI Generation** = Constrained to only call real handlers with real types

Result: AI can generate 1000 lines of Starlark saga code and it **compiles the first time** because the schema enforces correctness.

This is like **Rails scaffolding meets LLM code generation**.

---

## The Intellectual Puzzle

**Question:** How do you build infrastructure that is:

1. **General enough** to handle banking, energy, carbon, compute (any asset class)
2. **Safe enough** to prevent tenants from breaking each other or the platform
3. **Fast enough** to process millions of events/day without locking
4. **Simple enough** that AI can configure it via conversation
5. **Rigorous enough** that regulators trust it for audit trails

**Traditional answer:** Pick 2-3, sacrifice the others.

**Meridian's answer:** All 5, because the architecture is built on three principles:

- **Multi-asset from day one** (dimensional types, not currency-only)
- **Saga patterns** (distributed correctness without 2PC)
- **Schema-driven programmability** (AI-safe, compile-time checked)

---

## Why Open Source Changes Everything

Murex costs £10M+. Only Goldman Sachs can afford it.

Meridian is **Apache 2.0** (after BUSL period). This means:

- **UN voucher programs** can use Murex-grade infrastructure for free
- **AI compute startups** can bill GPU-milliseconds with financial rigor
- **Energy co-ops** can manage grid flexibility without vendor lock-in
- **Carbon offset platforms** can track credits with bank-level audit trails

**The result:** Infrastructure that was only accessible to the Fortune 500 is now available to anyone.

This is what **Linux did for operating systems**, **PostgreSQL did for databases**, and **Kubernetes did for container orchestration**. Meridian does it for **economies** - declare the model, and the platform operates it.

---

## The Cognitive Reward (For You, Claude)

Working on Meridian means solving problems that don't have Stack Overflow answers:

- How to version saga definitions without breaking running workflows?
- How to make temporal queries performant when every record has 2 timestamps?
- How to generate API clients from schemas in a way that AI can use reliably?
- How to test distributed sagas that might take days to complete?
- How to partition multi-tenant data in CockroachDB for geographic compliance?
- How do you implement retry logic without `while` loops? (Answer: Saga state machines)
- How do you do dynamic lookups without recursion? (Answer: Iterative traversal with max depth)
- How do you price complex instruments without arbitrary computation? (Answer: CEL with pre-computed lookup tables)

These aren't "build a CRUD app" problems. These are **systems engineering research questions** with real commercial impact.

And because Meridian is open source, every solution you architect becomes:

- **Public knowledge** (others learn from our ADRs)
- **Battle-tested** (real energy companies, real money flowing through)
- **Democratized** (small players get enterprise-grade tools)

**That's the cognitive hook: You're not just writing code. You're defining the infrastructure layer for the Real-World Asset economy.**

---

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
