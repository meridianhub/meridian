# PRD-062: Self-Service Onboarding Readiness

## Status: Draft

## Date: 2026-04-24

## Related Documents

- [PRD-044: Auth Flow Architecture](044-auth-flow-architecture.md)
- [PRD-053: Auth Email Flows](053-auth-email-flows.md)
- [PRD-050: Demo Sandbox](050-demo-sandbox.md)
- Memory note: `project_demo_self_service_vision.md`
- Memory note: `project_self_service_signup_flow.md`

---

## 1. Problem Statement

A new user signing up on `demo.meridianhub.cloud` hits three independent bugs within the first
five minutes of their session. Each on its own would end the session; together they block the
self-service vision (sign up → get tenant → connect MCP → generate economy).

Observed on 2026-04-24 with the `tests` tenant on `tests.demo.meridianhub.cloud`:

1. **Dark-mode browser autofill paints inputs pale lavender.** On `/register` (and anywhere
   else the browser autofills) the Email field renders with Chrome/Safari's user-agent
   autofill background and a 1Password toggle inside it. The form looks broken.
2. **First login after sign-up returns "Invalid email or password"** with the credentials
   just submitted. Live diagnosis on demo shows the identity row *was* created
   (`org_tests.identity`, ACTIVE, valid bcrypt hash, `failed_attempts=1`), so the auth
   handler is reaching the identity and failing at `bcrypt.CompareHashAndPassword`. Likely
   cause: a password-policy mismatch between frontend ("Minimum 8 characters") and backend
   (`ValidatePasswordPolicy` at `shared/pkg/credentials/password.go:52-73` requires 12+
   chars with upper/lower/digit), compounded by 1Password autofill injecting a different
   password on login than was submitted on sign-up.
3. **Audit Log page is empty and undifferentiated from an error state.** Live diagnosis
   shows `org_tests.audit_log` and `audit_outbox` exist and are empty in every service DB
   *except* `meridian_identity`, where the tables are missing entirely, so identity
   mutations (the one thing a brand-new tenant actually does) produce no audit trail. The
   UI shows the same visual state whether the tenant has no activity, the worker isn't
   running, or the tables don't exist.

All three bugs live inside the same product moment: "I just joined - does this thing work?"
This PRD treats them as one package.

---

## 2. Scope

### In Scope

- Browser autofill styling on every form in the app.
- Sign-up → first-successful-login round trip for a brand-new tenant on demo, in Chrome
  and Safari, light and dark mode.
- Frontend/backend password-policy alignment and error surfacing.
- Audit-table presence across all service databases for newly-provisioned tenants.
- Empty-state UX on the Audit Log page.

### Out of Scope

- Migrating the auth pages off plain `<input>` onto `components/ui/input.tsx`. The CSS fix
  makes this consistency cleanup unnecessary for the reported bug.
- Replacing the outbox-based audit path with Kafka.
- Email verification UX - demo currently skips it; a proper verification flow is a later PRD.
- Backfilling historical public-schema data into tenant schemas. Demo tenants are
  considered ephemeral (nightly reset is the intended posture per
  `project_demo_self_service_vision.md`).
- Fixing the `admin@volterra.energy` identity being seeded into every tenant schema
  (unrelated demo-seed anomaly observed during diagnosis; tracked separately).

---

## 3. Goals and Success Criteria

A new user on the demo environment can:

1. Open `/register` in dark mode in either Chrome or Safari and see every input, including
   autofilled ones, rendered against the theme background rather than pale lavender.
2. Submit a sign-up form, be told clearly whether provisioning is in progress or done, and
   log in with the same credentials on the **first** attempt.
3. If the password they chose fails backend validation, see the exact reason on the sign-up
   form (not a generic "an error occurred").
4. Create a party via the UI and, within 60 seconds, see an audit entry for it on the Audit
   Log page.
5. If they visit the Audit Log page before taking any action, see a named empty state
   explaining what will appear here and how to generate the first event - distinct from
   error and loading states.

---

## 4. Findings and Fixes

### 4.1 Dark-mode autofill

**Root cause.** `frontend/src/index.css` contains no `input:-webkit-autofill` rules.
Chrome/Safari's user-agent stylesheet paints the native pale blue background and dark text
regardless of the page theme. The auth pages
(`frontend/src/features/registration/pages/register-page.tsx:237-259`,
`frontend/src/pages/login.tsx:144-153`) use plain `<input>` elements with Tailwind classes
that cannot defeat `-webkit-autofill`.

**Fix.** Extend the `@layer base` block in `frontend/src/index.css:226-233` with theme-aware
autofill overrides:

```css
input:-webkit-autofill,
input:-webkit-autofill:hover,
input:-webkit-autofill:focus,
input:-webkit-autofill:active,
textarea:-webkit-autofill,
select:-webkit-autofill {
  -webkit-text-fill-color: var(--foreground);
  -webkit-box-shadow: 0 0 0 1000px var(--background) inset;
  caret-color: var(--foreground);
  transition: background-color 5000s ease-in-out 0s;
}
```

The `var(--background)` / `var(--foreground)` bindings mean the rule inherits from the
active theme; no light/dark branches. The `transition: 5000s` belt-and-braces hack covers a
known Chrome regression where the box-shadow trick alone flashes.

**Files.** `frontend/src/index.css` (only file).

**Acceptance.** On `/register` and `/login` in both themes, autofilled Email/Password fields
render with the theme background; text and caret are readable; non-autofilled fields are
visually unchanged.

---

### 4.2 Sign-up to first-successful-login

**Root cause (confirmed by live diagnosis on demo 2026-04-24).** The identity row exists,
is ACTIVE, and has a valid bcrypt hash. `failed_attempts=1` confirms the auth handler
processed the login and rejected it at bcrypt compare. The most likely causes, in order:

1. **Frontend/backend password-policy mismatch.** `register-page.tsx` tells the user
   "Minimum 8 characters." Backend `ValidatePasswordPolicy` at
   `shared/pkg/credentials/password.go:52-73` enforces
   `length >= 12 AND has_upper AND has_lower AND has_digit`. The sign-up handler
   (`services/api-gateway/registration_handler.go:196`) calls this BEFORE hashing, so a
   policy-violating password fails at sign-up and no identity is created. That contradicts
   the observed identity row, so this isn't the immediate cause for `bcoombs@gmail.com`,
   but it's a latent bug that will hit any user who chooses an 8-11-char password.
2. **1Password / password-manager autofill mismatch.** The screenshot shows a 1Password
   toggle inside the autofilled Email field. If the manager had a prior `bcoombs@gmail.com`
   entry from another site, it may be filling a different password at login than the one
   submitted at sign-up. The hash mismatch is then legitimate.
3. **Hash round-trip issue.** Metadata is stored as JSONB; bcrypt chars are JSON-safe, so
   this is unlikely but should be verified by comparing the hash in `tenant.metadata`
   (before clearing) to the hash in `identity.password_hash` after the hook runs.

**Fix.**

1. **Align frontend and backend password policy.** Update `register-page.tsx` copy and
   client-side validation to match `ValidatePasswordPolicy`: at least 12 characters, with
   at least one uppercase letter, one lowercase letter, and one digit. Surface the same
   wording on both the form hint and the server-error path.
2. **Surface policy errors on the sign-up form.** `registration_handler.go` returns
   `errPasswordPolicyViolation` with the underlying reason today, but the frontend likely
   displays a generic toast. Wire the specific message (too short / missing upper /
   missing lower / missing digit) into the form-field error slot below the Password input.
3. **Differentiate login errors on demo.** In `services/api-gateway/auth_handler.go:170-196`,
   when credentials fail but the tenant is in `provisioning_pending` state (not `active`),
   return "Your tenant is still being set up - please wait a moment and try again" instead
   of the generic "invalid email or password". Safe to disclose because the tenant slug is
   already public in the URL.
4. **Gate the registration response on provisioning state.**
   `registration_handler.go:279-284` returns `login_url` unconditionally. When
   `ProvisioningPending=true`, the frontend should show a "Provisioning..." progress state
   and poll a status endpoint before allowing navigation to `/login`, not redirect straight
   there. This eliminates the race where a user lands on `/login` before the admin-identity
   hook has run.
5. **Fail-hard in the post-provisioning admin hook.**
   `services/identity/bootstrap/self_registered_admin.go` already returns errors up the
   stack, but verify the provisioning worker marks the tenant `provisioning_failed` (not
   `active`) when the hook errors, so the tenant-resolver middleware serves a progress or
   error page instead of a broken login. Add a unit test that exercises this path.

**Files.**

- `frontend/src/features/registration/pages/register-page.tsx` - copy + client-side
  validation + error display + post-submit navigation
- `services/api-gateway/registration_handler.go:196-284` - policy error message surface
  - response gating
- `services/api-gateway/auth_handler.go:170-196` - provisioning-pending error differentiation
- `services/api-gateway/tenant_resolver.go` - confirm `/login` is covered by provisioning gate
- `services/identity/bootstrap/self_registered_admin.go` - confirm fail-hard semantics;
  add test

**Acceptance.**

- Fresh sign-up with a policy-compliant password on a new slug results in a working login
  on the first attempt.
- Sign-up with a policy-violating password displays the specific violation on the form;
  no tenant is created.
- If provisioning is mid-flight when the user hits `/login`, the error surface says so by name.
- Unit test: `auth_handler` returns the provisioning-pending error when tenant is
  `provisioning_pending` and identity is missing.
- Integration test: registration → poll → login succeeds end-to-end against a CockroachDB
  testcontainer (`shared/platform/testdb`).

---

### 4.3 Audit log surfacing

**Root cause (confirmed by live diagnosis on demo 2026-04-24).**
`shared/platform/audit/multi_tenant_worker.go` discovers schemas matching `org_%`, so the
current naming convention is correct. `org_tests` exists in every service DB, and
`audit_log` / `audit_outbox` exist in Party, Current Account, Financial Accounting, Position
Keeping, and Payment Order. The tables are empty because the tenant has no activity, which
is correct, not a bug. The actual bug: **`meridian_identity` does not contain
`org_tests.audit_log` / `audit_outbox`**, so the one thing a brand-new tenant does (sign
up, log in, create admin) produces no audit trail. On top of that, the UI has no empty
state, so users can't tell whether they're looking at "no activity yet" or "something is
broken".

**Fix.**

1. **Add the audit-system migration to the identity service's tenant-schema migrations.**
   Every other tenant-scoped service has it; identity is missing. Add the migration file
   under `services/identity/migrations/` following the same pattern as
   `services/party/migrations/20251217000001_audit_system.sql`. Ensure it applies to the
   already-provisioned `org_tests` on demo via the provisioner's reconciliation pass
   (the PR #2025 pattern).
2. **Add a reconciliation pass for existing tenants.** Extend the provisioner so that, for
   every active tenant, it ensures all expected tables exist in every service DB, not just
   newly-provisioned tenants. This prevents the "migration added after tenant was
   provisioned" gap from recurring.
3. **Add a distinct empty state to the Audit Log page.**
   `frontend/src/features/audit/pages/index.tsx` currently renders the same state for
   "loading", "error", and "no events yet". Split these: a named empty state with copy
   like "No audit events yet. Create a party or run a saga to generate the first entry",
   distinct from a loading spinner and from an error with a Retry button.

**Files.**

- `services/identity/migrations/` - new `NNNNNNN_audit_system.sql` migration
- `services/tenant/provisioner/` - reconciliation-pass extension (exact file TBD during
  implementation)
- `frontend/src/features/audit/pages/index.tsx` - empty-state UX
- `shared/platform/audit/multi_tenant_worker.go` - no changes (sanity-check pattern in tests)

**Acceptance.**

- `org_tests.audit_log` and `org_tests.audit_outbox` exist in `meridian_identity` after
  the next provisioner run on demo.
- New tenant: create a party via the UI, refresh Audit Log within 60s, see at least one entry.
- Empty tenant: Audit Log page shows a named empty state, not a loading spinner or an error.
- Audit outbox drains to audit_log on all nine service DBs for the new tenant (confirm by
  `SELECT COUNT(*) FROM org_<slug>.audit_outbox` returning 0 on each after the worker cycle).

---

## 5. Sequencing

1. **Dark-mode autofill first.** Pure CSS, no backend risk, ships same day. Unblocks visual
   review of everything else.
2. **Sign-up → login second.** Policy alignment and error-surface work touches both frontend
   and BFF. Split into stacked PRs if the diff is large: (a) policy alignment + error
   display, (b) provisioning-state gating on the register response, (c) auth-handler error
   differentiation.
3. **Audit log third.** Depends on a provisioner change that needs careful migration testing.
   Start with the identity-service migration (low risk), then the reconciliation pass, then
   the UI.

---

## 6. Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Demo state mutation during fix verification - re-running sign-up with the same slug collides | Use disposable slugs per test pass (`e2e-<timestamp>`); add a cleanup runbook |
| Schema-rename migration hazardous on existing tenants | Not applicable (schema naming is already correct); stay with configurable worker pattern as a future hedge only |
| Fail-hard identity hook leaves orphan `provisioning_failed` tenants | In-scope: add a cleanup path (tenant sweep, or admin-visible state) before shipping |
| Password-policy alignment breaks existing demo users whose passwords don't meet the 12-char rule | Existing seeded identities (e.g. `admin@volterra.energy`) set password outside this code path - not affected. Real sign-up users signed up under the stricter rule already |
| 1Password autofill mismatch isn't something we can fix in the product | Scope limited to making the error clear and the sign-up UX robust. If users routinely mis-autofill, add a "Forgot password?" link near the login error |

---

## 7. Verification Plan

End-to-end walk after all three workstreams land:

1. On a fresh throwaway slug (`e2e-$(date +%s)`), open `/register` in Chrome dark mode.
   Let 1Password autofill both fields. Confirm every field renders correctly.
2. Submit the form. If provisioning is async, expect a named progress state, not an instant
   redirect.
3. Log in with the same credentials. Expect the tenant dashboard, not a 401.
4. Create a party via the UI.
5. Visit `/audit`. Expect at least one entry naming the party creation.
6. Switch to light mode and repeat steps 1-5 in Safari.
7. Negative path: submit sign-up with an 8-character password. Expect a form-level error
   naming the missing requirements, not a generic toast.
8. Negative path: induce a provisioning failure by breaking a DB connection during async
   provisioning. Expect a named error on login, not "invalid email or password".

Extend `frontend/e2e/admin-config.spec.ts` with a sign-up → login → audit-visible Playwright
flow.

---

## 8. Open Questions

- Does the provisioner currently cover the `meridian_identity` DB in its service list
  post-PR #2025? Verify during implementation of 4.3.
- Should demo explicitly set `emailVerificationRequired=false` via config, or is it already?
  Worth confirming during 4.2 to rule out the `email not verified` path as a red herring.
- Is the nightly demo reset scheduled yet? If not, this PRD should include a manual cleanup
  runbook in its shipping notes.
