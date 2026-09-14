package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identitybootstrap "github.com/meridianhub/meridian/services/identity/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

// ErrLoginFailed is returned when the gateway rejects the seed credentials.
var ErrLoginFailed = errors.New("gateway login failed")

// ErrApplyManifestHTTP is returned when the manifest apply endpoint returns a non-2xx status.
var ErrApplyManifestHTTP = errors.New("apply manifest request failed")

// manifestApplyPath is the REST route Vanguard transcodes onto
// ApplyManifestService/ApplyManifest (see the google.api.http annotation in
// api/proto/meridian/control_plane/v1/apply_manifest_service.proto).
const manifestApplyPath = "/v1/manifests/apply"

// loginPath is the gateway BFF password-login endpoint. It is pre-auth: the
// tenant resolver runs, the auth middleware does not.
const loginPath = "/api/auth/login"

// seedAuth carries the credentials seed-dev uses to authenticate against the
// gateway. A zero value means no admin credentials are configured, in which case
// seed-dev applies the manifest unauthenticated (valid only when the server runs
// with AUTH_ENABLED=false - see ensureTenantAdmin).
type seedAuth struct {
	email    string
	password string
}

// configured reports whether platform admin credentials are available.
func (a seedAuth) configured() bool { return a.email != "" && a.password != "" }

// loadSeedAuth reads the platform admin credentials. These are the same
// variables identitybootstrap uses, so seed-dev authenticates as the identity
// the tenant provisioner already creates rather than inventing a second
// privileged account.
func loadSeedAuth() seedAuth {
	return seedAuth{
		email:    os.Getenv("PLATFORM_ADMIN_EMAIL"),
		password: os.Getenv("PLATFORM_ADMIN_PASSWORD"),
	}
}

// ensureTenantAdmin provisions the platform admin identity into the target
// tenant's schema so seed-dev can log in as a principal that satisfies the
// ApplyManifest role requirement.
//
// ApplyManifest requires auth.RoleAdmin (control-plane interceptors.go). The
// platform admin carries RolePlatformAdmin/RoleSuperAdmin/RoleTenantOwner, all
// of which outrank RoleAdmin in the control-plane role hierarchy, so it clears
// the check without granting a bespoke role.
//
// This runs after the tenant schema is provisioned and before the manifest is
// applied. It is idempotent: an existing admin has its roles reconciled rather
// than being recreated. It is a no-op when PLATFORM_ADMIN_* are unset.
func ensureTenantAdmin(ctx context.Context, auth seedAuth, tid string) error {
	if !auth.configured() {
		fmt.Println("  Platform admin credentials not set; skipping tenant admin provisioning.")
		return nil
	}

	baseDSN := os.Getenv("DATABASE_URL")
	if baseDSN == "" {
		// Direct database reachability is a convenience, not a requirement: the
		// same admin is provisioned into every tenant schema by the
		// post-provisioning hook (wire_services.go), and runSeed waits for the
		// tenant to reach ACTIVE before this runs. Failing here would block
		// seed-dev anywhere it can reach the gateway but not the database.
		fmt.Println("  DATABASE_URL not set; relying on the post-provisioning hook for the tenant admin.")
		return nil
	}

	// The identity repo lives in meridian_identity, while DATABASE_URL points at
	// the platform database - the same derivation seedDemoOperator performs.
	identityDSN, err := replaceDatabase(baseDSN, "meridian_identity")
	if err != nil {
		return fmt.Errorf("derive identity DSN: %w", err)
	}

	db, err := bootstrap.NewDatabase(ctx, bootstrap.DatabaseConfig{
		DSN:          identityDSN,
		MaxOpenConns: 2,
		MaxIdleConns: 1,
	})
	if err != nil {
		return fmt.Errorf("connect to identity database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get underlying sql.DB for cleanup: %w", err)
	}
	defer sqlDB.Close()

	tenantID, err := tenant.NewTenantID(tid)
	if err != nil {
		return fmt.Errorf("invalid tenant ID %q: %w", tid, err)
	}

	repo := identitypersistence.NewRepository(db)
	if err := identitybootstrap.ProvisionAdminForTenant(ctx, repo, tenantID); err != nil {
		return fmt.Errorf("provision tenant admin: %w", err)
	}

	fmt.Printf("  Platform admin provisioned in tenant %s.\n", tid)
	return nil
}

// setTenantRouting makes the request resolvable by the gateway's tenant resolver
// in both of its modes.
//
// The resolver accepts X-Tenant-Slug only when LOCAL_DEV_MODE is set
// (tenant_resolver.go extractSlugFromRequest); otherwise it falls back to
// extractSlug(r.Host), which requires a host ending in ".<BASE_DOMAIN>". Deployed
// environments run LOCAL_DEV_MODE=false while seed-dev still dials the gateway on
// localhost, so sending only the header resolves nothing and the request is
// rejected with 404 "Invalid subdomain".
//
// Setting req.Host overrides the Host header independently of the dial address,
// so the connection still goes to --gateway-url while the resolver sees the
// tenant's subdomain. The header is kept for LOCAL_DEV_MODE, where the derived
// host may not match the configured base domain.
func setTenantRouting(req *http.Request, slug, host string) {
	if slug != "" {
		req.Header.Set("X-Tenant-Slug", slug)
	}
	if host != "" {
		req.Host = host
	}
}

// warnOnCleartextCredentials warns when credentials would cross a plaintext
// connection to a non-loopback host. seed-dev normally runs against the gateway
// on localhost inside the same container or pod, where plaintext is expected;
// anything else is worth surfacing rather than silently sending a platform admin
// password in the clear.
func warnOnCleartextCredentials(gateway string) {
	parsed, err := url.Parse(gateway)
	if err != nil || parsed.Scheme == "https" {
		return
	}
	hostname := parsed.Hostname()
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" {
		return
	}
	fmt.Printf("  WARNING: %s is not HTTPS and not loopback; credentials and the\n", gateway)
	fmt.Println("  bearer token will cross the network in cleartext.")
}

// loginResponse mirrors the gateway's BFF login response body.
type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// login exchanges the platform admin credentials for a Meridian-signed JWT.
//
// The gateway is the single JWT enforcement point: it validates the token on the
// way in and re-emits the verified identity as x-user-id / x-auth-roles /
// x-tenant-id gRPC metadata, which the unified binary reconstructs into claims
// for manifest RBAC. Presenting a token here is therefore what makes the apply
// authorized - the token itself is never seen by the gRPC server.
func login(ctx context.Context, client *http.Client, gateway string, auth seedAuth, slug, host string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"email":    auth.email,
		"password": auth.password,
	})
	if err != nil {
		return "", fmt.Errorf("marshal login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(gateway, "/")+loginPath, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setTenantRouting(req, slug, host)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read login response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: HTTP %d: %s", ErrLoginFailed, resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var parsed loginResponse
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("parse login response: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", fmt.Errorf("%w: response carried no access token", ErrLoginFailed)
	}

	return parsed.AccessToken, nil
}

// applyManifestHTTP applies the manifest through the gateway rather than by
// dialing the loopback gRPC server directly.
//
// The loopback gRPC server is built WithoutAuth() (cmd/meridian/main.go), so it
// never parses a bearer token - a JWT attached to a direct gRPC call would be
// ignored and manifest RBAC would still deny the request. Routing through the
// gateway's transcoded REST route is what gets the caller's identity verified
// and forwarded as the metadata the RBAC interceptor reads.
//
// An empty token sends the request unauthenticated, which succeeds only when the
// server runs with AUTH_ENABLED=false.
func applyManifestHTTP(
	ctx context.Context,
	client *http.Client,
	gateway, slug, host, token, path string,
	force bool,
) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest file: %w", err)
	}

	var manifest controlplanev1.Manifest
	if err := protojson.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest JSON: %w", err)
	}

	applyReq := &controlplanev1.ApplyManifestRequest{
		Manifest:  &manifest,
		DryRun:    false,
		AppliedBy: "seed-dev",
		Force:     force,
	}

	body, err := protojson.Marshal(applyReq)
	if err != nil {
		return fmt.Errorf("marshal apply request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(gateway, "/")+manifestApplyPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build apply request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setTenantRouting(req, slug, host)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("apply manifest request: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read apply response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(payload))
		// A denial while sending no token is the specific, recoverable
		// misconfiguration: the server enforces manifest RBAC (AUTH_ENABLED=true)
		// but seed-dev had no credentials to authenticate with. Name the fix
		// rather than surfacing a bare 401.
		if token == "" && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			fmt.Println("  The server enforces manifest RBAC but seed-dev sent no credentials.")
			fmt.Println("  Set PLATFORM_ADMIN_EMAIL and PLATFORM_ADMIN_PASSWORD so seed-dev can")
			fmt.Println("  authenticate, or run the server with AUTH_ENABLED=false for local development.")
			return fmt.Errorf("%w: HTTP %d: %s (no PLATFORM_ADMIN_EMAIL configured)",
				ErrApplyManifestHTTP, resp.StatusCode, detail)
		}
		return fmt.Errorf("%w: HTTP %d: %s", ErrApplyManifestHTTP, resp.StatusCode, detail)
	}

	var applyResp controlplanev1.ApplyManifestResponse
	// The transcoder may include fields this binary's descriptors predate;
	// tolerate them rather than failing the seed on an additive proto change.
	unmarshaler := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshaler.Unmarshal(payload, &applyResp); err != nil {
		return fmt.Errorf("parse apply response: %w", err)
	}

	return reportApplyResult(&applyResp)
}

// reportApplyResult prints the apply outcome and converts it into an error when
// the manifest did not apply cleanly.
func reportApplyResult(resp *controlplanev1.ApplyManifestResponse) error {
	fmt.Printf("  Job ID: %s\n", resp.GetJobId())
	fmt.Printf("  Status: %s\n", resp.GetStatus().String())
	if diff := resp.GetDiffSummary(); diff != "" {
		fmt.Printf("  Changes: %s\n", diff)
	}
	if len(resp.GetValidationErrors()) > 0 {
		fmt.Printf("  Validation errors: %d\n", len(resp.GetValidationErrors()))
		for _, ve := range resp.GetValidationErrors() {
			fmt.Printf("    [%s] %s: %s\n", ve.GetSeverity(), ve.GetPath(), ve.GetMessage())
		}
		return fmt.Errorf("%w: %d error(s)", ErrManifestValidation, len(resp.GetValidationErrors()))
	}

	// Print step results for debugging (visible in CI logs).
	for _, sr := range resp.GetStepResults() {
		fmt.Printf("  Step [%s]: %s — %s\n", sr.GetStepName(), sr.GetStatus().String(), sr.GetMessage())
		for k, v := range sr.GetDetails() {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}
	for phase, detail := range resp.GetPhaseStatus() {
		fmt.Printf("  Phase [%s]: %s %s\n", phase, detail.GetStatus(), detail.GetError())
	}

	// Check response status — a nil-executor or saga failure returns a non-success status.
	switch resp.GetStatus() { //nolint:exhaustive // default catches future enum additions
	case controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
		controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN:
		// success
	default:
		return fmt.Errorf("%w: %s", ErrManifestApplyFailed, resp.GetStatus().String())
	}

	fmt.Println("Manifest applied successfully.")
	return nil
}

// newSeedHTTPClient returns the HTTP client used for gateway calls.
func newSeedHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// applyManifestGRPC applies the manifest by dialing the control-plane directly.
//
// This is the Tilt topology: services run standalone, so the gateway is
// services/api-gateway/cmd, which constructs its server with a nil tenant
// resolver and wires no Vanguard transcoder - /v1/manifests/apply does not exist
// there. The standalone control-plane instead runs the usual JWT auth
// interceptor alongside manifest RBAC (services/control-plane/cmd/main.go), so a
// token belongs in gRPC metadata where that interceptor can validate it.
//
// Selected by --control-plane-addr. Everything else goes through the gateway.
func applyManifestGRPC(ctx context.Context, conn *grpc.ClientConn, tid, token, path string, force bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest file: %w", err)
	}

	var manifest controlplanev1.Manifest
	if err := protojson.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest JSON: %w", err)
	}

	pairs := []string{"x-tenant-id", tid}
	if token != "" {
		pairs = append(pairs, "authorization", "Bearer "+token)
	}
	callCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(pairs...))

	client := controlplanev1.NewApplyManifestServiceClient(conn)
	resp, err := client.ApplyManifest(callCtx, &controlplanev1.ApplyManifestRequest{
		Manifest:  &manifest,
		DryRun:    false,
		AppliedBy: "seed-dev",
		Force:     force,
	})
	if err != nil {
		return fmt.Errorf("ApplyManifest RPC: %w", err)
	}

	return reportApplyResult(resp)
}
