---
name: adr-0037-scheduler-attribution-design
description: Attribute scheduled operations to distinct actor identities rather than anonymous system context
triggers:
  - Implementing or modifying scheduled job execution in any service
  - Adding new scheduler or worker background process
  - Reviewing audit trail completeness for SOC 2 or ISO 27001
  - Designing actor identity for non-human system operations
instructions: |
  Schedulers must inject an Actor into the execution context before calling any
  downstream service. Use auth.WithActor with Type=ActorTypeScheduler,
  Authenticated=false, and ID in the format "system:scheduler:{scheduler-name}".
  Never set Authenticated=true in schedulers or workers - only the gRPC auth
  interceptor may set that field. Use a separate actorContextKey (not UserIDContextKey)
  to prevent scheduler identity from being mistaken for an authenticated user session.
---

# 37. Scheduler Attribution Design

Date: 2026-04-07

## Status

Accepted

## Context

Meridian's scheduler infrastructure executes jobs on behalf of tenants using
`context.Background()`. Without additional identity context, any mutations
performed during execution are recorded with `changed_by = "system"` - an
anonymous attribution that fails two compliance requirements:

- **SOC 2 CC6.1**: Logical and physical access controls must identify the
  actor responsible for each operation.
- **ISO 27001 A.5.16**: Identity management requires that operations be
  attributable to a specific, identifiable actor.

When the audit trail shows `changed_by = "system"` for all scheduler-triggered
mutations, it is impossible to distinguish human operations from scheduled
operations, or to identify which scheduler triggered a particular change.

### The Trust Escalation Risk

The identity service uses `GetUserIDFromContext` as an authentication gate:

```go
// services/identity/service/grpc_identity_endpoints.go:129
if _, ok := auth.GetUserIDFromContext(ctx); !ok {
    return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
}

// services/identity/service/grpc_role_endpoints.go:147
if _, ok := auth.GetUserIDFromContext(ctx); !ok {
    return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
}
```

If a scheduler identity were placed in `UserIDContextKey` (the same key used
by the gRPC auth interceptor for JWT-validated sessions), scheduler jobs would
bypass authentication checks and gain access to identity-management endpoints.
This would violate the principle of least privilege and allow a misconfigured
scheduler to perform privileged operations.

### Phase Design

Attribution is introduced in two phases:

- **Phase A (current)**: Attributed identity. The scheduler asserts its own
  identity string. The claim is not cryptographically verified - it is trusted
  because it originates from platform-controlled code, not from external input.
- **Phase C (deferred)**: Authenticated identity. Each scheduler acquires a
  short-lived JWT from the platform's identity provider. The gRPC auth
  interceptor verifies the token and sets `Authenticated=true`.

## Decision Drivers

* Audit trails must identify which scheduler triggered each mutation for
  SOC 2 and ISO 27001 compliance
* Scheduler identity must not be mistakable for an authenticated human session
* `Actor.Authenticated=false` must be preserved to prevent privilege escalation
  through trust promotion
* The design must be forward-compatible with Phase C JWT-based authentication
* Multiple scheduler instances (billing, settlement, catch-up) must produce
  distinguishable audit records

## Considered Options

1. **Attributed identity with separate context key** (chosen)
2. **Attributed identity reusing UserIDContextKey**
3. **Defer all attribution to Phase C JWT authentication**
4. **Service account per scheduler with full JWT issuance**

## Decision Outcome

Chosen option: **Attributed identity with separate context key**, because it
satisfies the immediate compliance requirements while preserving the security
boundary between scheduler and authenticated-user identity paths.

### Implementation

The `Actor` struct in `shared/platform/auth/actor.go` carries four fields:

| Field | Purpose |
|-------|---------|
| `ID` | Identifier string, e.g. `system:scheduler:billing-cron` |
| `Type` | `ActorTypeScheduler`, `ActorTypeWorker`, `ActorTypeHuman`, etc. |
| `Authenticated` | `false` for all schedulers; `true` only when set by the gRPC auth interceptor |
| `Source` | Describes the injection path, e.g. `"cron-scheduler"`, `"catch-up"` |

The `actorContextKey` type is an unexported struct distinct from `contextKey`
(used for `UserIDContextKey`). This structural separation is the enforcement
mechanism: `auth.GetUserIDFromContext` cannot retrieve an Actor, and
`auth.ActorFromContext` cannot retrieve a user ID. The two identity channels
cannot collide regardless of the values they carry.

The Actor is injected in `executeJob` (live cron execution) and
`catchUpSchedule` (startup catch-up) before any downstream call:

```go
// shared/platform/scheduler/cron.go - executeJob
ctx = auth.WithActor(ctx, auth.Actor{
    ID:            fmt.Sprintf("system:scheduler:%s", s.config.Name),
    Type:          auth.ActorTypeScheduler,
    Authenticated: false,
    Source:        "cron-scheduler",
})

// shared/platform/scheduler/catchup.go - catchUpSchedule
ctx = auth.WithActor(ctx, auth.Actor{
    ID:            fmt.Sprintf("system:scheduler:%s", s.config.Name),
    Type:          auth.ActorTypeScheduler,
    Authenticated: false,
    Source:        "catch-up",
})
```

The `GetUserFromContext` function in `shared/platform/audit/context.go` checks
for an Actor first, then falls back to `UserIDContextKey`, then to
`DefaultAuditUser`:

```go
if actor, ok := auth.ActorFromContext(ctx); ok && actor.ID != "" {
    return actor.ID
}
userID, ok := auth.GetUserIDFromContext(ctx)
// ...
return DefaultAuditUser
```

The `changed_by` column in audit tables will contain
`system:scheduler:{scheduler-name}` for all scheduler-triggered mutations,
making them distinguishable from human operations (`<user-uuid>`) and from
genuinely anonymous operations (`system`).

The scheduler name is injected at construction time via `CronSchedulerConfig.Name`,
meaning different scheduler instances (e.g., `billing-cron`, `settlement-cron`)
produce distinct attribution strings without any shared configuration.

Tenant ID is not included in the `changed_by` string. The audit trail is
already scoped to the tenant schema; including the tenant ID in `changed_by`
would denormalise data that is implicit from the row's location.

### Positive Consequences

* Audit trails distinguish scheduler-triggered mutations from human operations
  and anonymous system operations, satisfying SOC 2 CC6.1 and ISO 27001 A.5.16
* `Actor.Authenticated=false` ensures schedulers cannot be promoted to
  authenticated status by any downstream code path
* Separate context key enforces the boundary at the type level; no runtime
  check can accidentally treat a scheduler actor as an authenticated user
* The `Source` field on `Actor` supports forensic analysis: catch-up executions
  (`"catch-up"`) are distinguishable from live cron executions (`"cron-scheduler"`)
  in diagnostic logs even when both carry the same `ID`
* The design is forward-compatible with Phase C: adding JWT issuance requires
  only setting `Authenticated=true` in the interceptor; no downstream code
  changes are needed

### Negative Consequences

* Attribution strings are asserted, not verified. A bug in platform code could
  inject an incorrect `Actor.ID`. Mitigation: attribution is set only in
  platform-controlled scheduler code, not at service boundaries or in
  tenant-configurable logic
* A tenant may be suspended after a scheduler acquires context but before
  execution completes. The tenant status check runs before semaphore acquisition
  and execution, but not continuously during execution. This is an acceptable
  window given that execution is bounded (default: 5 minutes)

## Pros and Cons of the Options

### Option 1: Attributed identity with separate context key (chosen)

* Good, because audit compliance is satisfied immediately without Phase C
* Good, because the structural key separation prevents scheduler identity from
  being mistaken for an authenticated session at the type level
* Good, because multiple schedulers produce distinct attribution without
  additional configuration
* Good, because `Actor.Authenticated=false` is a stable invariant that
  downstream authorization checks can rely on
* Bad, because attribution strings are not cryptographically verified

### Option 2: Attributed identity reusing UserIDContextKey

* Good, because no new context key needed
* Bad, because scheduler identity would pass the `GetUserIDFromContext` auth
  gate in identity endpoints, granting schedulers unintended access to
  privileged operations
* Bad, because audit trail cannot distinguish scheduler from authenticated
  human user without inspecting the ID format string

### Option 3: Defer all attribution to Phase C JWT authentication

* Good, because authenticated identity is stronger than attributed identity
* Bad, because compliance gap persists until Phase C is complete
* Bad, because Phase C requires identity provider integration, token issuance
  infrastructure, and interceptor changes - a multi-week effort
* Bad, because a scheduler with no identity at all fails SOC 2 CC6.1 today

### Option 4: Service account per scheduler with full JWT issuance

* Good, because each scheduler has a cryptographically verified identity today
* Bad, because requires standing up service account management, token issuance,
  and rotation infrastructure before any compliance benefit is realised
* Bad, because overkill for the current threat model: schedulers run in
  platform-controlled code, not at tenant-configurable boundaries

## Links

* PR #2151 - Update audit context to check Actor for scheduler attribution
* PR #2163 - Inject Actor and correlation ID in scheduler executeJob
* `shared/platform/auth/actor.go` - Actor struct and actorContextKey
* `shared/platform/audit/context.go` - GetUserFromContext with Actor check
* `shared/platform/scheduler/cron.go` - Actor injection in executeJob
* `shared/platform/scheduler/catchup.go` - Actor injection in catchUpSchedule

## Notes

* **Phase C trigger**: When service accounts or machine-identity JWTs are
  introduced, the scheduler should acquire a short-lived token from the
  identity provider at startup and refresh it on expiry. The gRPC auth
  interceptor would verify the token and set `Authenticated=true`. No changes
  to the `Actor` struct, context keys, or downstream audit code are required.
* **New scheduler checklist**: Any new background worker or scheduler must
  inject an `Actor` with `Authenticated=false` before its first downstream
  call. Omitting this reverts attribution to `"system"` and reopens the
  compliance gap.
* **Do not copy Authenticated from external input**: The `Actor.Authenticated`
  field must never be populated from proto messages, HTTP headers, or request
  bodies. Only the gRPC auth interceptor may set it to `true`.
