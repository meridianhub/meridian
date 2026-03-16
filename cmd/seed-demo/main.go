// Package main is a demo data seeder for the Meridian energy platform demo.
//
// It connects to the Meridian gRPC server and seeds:
//   - An energy company tenant ("volterra-energy")
//   - The Volterra Energy demo manifest via ApplyManifest (structural provisioning):
//   - Instruments: GBP, KWH, CARBON_CREDIT
//   - Account types: ENERGY_TRADING, INVENTORY_KWH, CARBON_INVENTORY, SETTLEMENT
//   - Market data source: SEED_DEMO
//   - Market data set: WHOLESALE_ENERGY_GBP_KWH
//   - Valuation rules: KWH→GBP, CARBON_CREDIT→GBP
//   - Organizations: UK Power Networks (DNO) + 4 Grid Supply Points
//   - Internal accounts: 4 GSP KWH inventory accounts (one per GSP)
//   - 10 residential customers each with:
//   - A GBP billing account (charges in pounds sterling)
//   - A KWH consumption tracking account (meter reading credits)
//   - 30 days of simulated meter reads using double-entry:
//   - CREDIT customer KWH account (asset: energy consumed)
//   - DEBIT GSP KWH inventory account (liability: energy owed to grid)
//   - 30 days of GBP billing at fixed retail tariff (24.5p/kWh)
//   - A wholesale energy price dataset with 30 days of historical prices
//
// Structural provisioning (instruments, account types, market data, organizations,
// and internal accounts) is handled entirely via ApplyManifest. This ensures
// idempotent reapply returns NO_CHANGE for structural resources without separate
// gRPC calls.
//
// Operational seeding (customer parties, accounts, deposits, and price observations)
// continues via direct gRPC calls since these are runtime data, not structural config.
//
// All operations are idempotent — safe to run multiple times.
//
// Usage:
//
//	seed-demo --grpc-addr=localhost:50051 --gateway-url=http://localhost:8090
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	marketv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	tenantID   = "volterra_energy"
	tenantSlug = "volterra-energy"
)

// Sentinel errors for idempotent lookups.
var (
	errPartyNotFoundInListing   = fmt.Errorf("party reported as existing but not found in listing")
	errAccountNotFoundInListing = fmt.Errorf("account reported as existing but not found in listing")
	errManifestValidationFailed = fmt.Errorf("manifest validation failed")
)

var (
	gatewayURL string
	grpcAddr   string
)

func main() {
	flag.StringVar(&gatewayURL, "gateway-url", envOrDefault("GATEWAY_URL", "http://localhost:8090"), "Gateway HTTP URL for health check")
	flag.StringVar(&grpcAddr, "grpc-addr", envOrDefault("GRPC_ADDR", "localhost:50051"), "gRPC server address")
	flag.Parse()

	if err := run(); err != nil {
		log.Fatalf("seed-demo failed: %v", err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 1. Wait for gateway health
	fmt.Printf("Waiting for gateway at %s ...\n", gatewayURL)
	err := await.New().
		AtMost(60 * time.Second).
		PollInterval(2 * time.Second).
		WithContext(ctx).
		Until(func() bool {
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/healthz", nil)
			if reqErr != nil {
				return false
			}
			resp, reqErr := http.DefaultClient.Do(req)
			if reqErr != nil {
				return false
			}
			_ = resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	if err != nil {
		return fmt.Errorf("gateway not healthy: %w", err)
	}
	fmt.Println("Gateway is healthy.")

	// 2. Connect to gRPC
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("gRPC connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// 3. Create tenant
	fmt.Println("\n=== Step 1: Create Energy Tenant ===")
	if err := createTenant(ctx, conn); err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}

	// 4. Apply demo manifest (structural provisioning via ApplyManifest).
	// This provisions all structural resources: instruments, account types,
	// market data sources/sets, valuation rules, organizations (DNO + GSPs),
	// and GSP KWH inventory internal accounts.
	// Re-running is idempotent: unchanged resources return NO_CHANGE status.
	fmt.Println("\n=== Step 2: Apply Demo Manifest (structural provisioning) ===")
	tCtx := withTenant(ctx)
	if err := applyManifest(tCtx, conn); err != nil {
		return fmt.Errorf("apply manifest: %w", err)
	}

	// 5. Resolve GSP KWH internal account IDs provisioned by the manifest.
	// Accounts are identified by the codes defined in volterra-energy-demo.json.
	fmt.Println("\n=== Step 3: Resolve GSP Internal Account IDs ===")
	gspKwhAccountIDs, err := resolveGSPInternalAccountIDs(tCtx, conn)
	if err != nil {
		return fmt.Errorf("resolve GSP internal accounts: %w", err)
	}

	// 6. Register customer parties (operational data — not in manifest).
	fmt.Println("\n=== Step 4: Register Customer Parties ===")
	customerPartyIDs, err := registerCustomerParties(tCtx, conn)
	if err != nil {
		return fmt.Errorf("register customer parties: %w", err)
	}

	// 7. Resolve the DNO party ID for the manifest-provisioned UKPN organization.
	// Customer current accounts reference the owning org via OrgPartyId.
	fmt.Println("\n=== Step 5: Resolve DNO Party ID ===")
	dnoPartyID, err := resolveOrganizationPartyID(tCtx, conn, "UKPNDNO001")
	if err != nil {
		return fmt.Errorf("resolve DNO party ID: %w", err)
	}
	fmt.Printf("  DNO party ID: %s\n", dnoPartyID)

	// 8. Create customer current accounts (operational data — not in manifest).
	fmt.Println("\n=== Step 6: Create Customer Accounts ===")
	customerAccounts, err := createAccounts(tCtx, conn, dnoPartyID, customerPartyIDs, gspKwhAccountIDs)
	if err != nil {
		return fmt.Errorf("create accounts: %w", err)
	}

	// 9. Deposit meter reads and billing amounts (operational data — not in manifest).
	fmt.Println("\n=== Step 7: Seed Account Balances ===")
	if err := seedBalances(tCtx, conn, customerAccounts); err != nil {
		return fmt.Errorf("seed balances: %w", err)
	}

	// 10. Record wholesale energy price observations (operational data — not in manifest).
	// The WHOLESALE_ENERGY_GBP_KWH dataset was provisioned by ApplyManifest;
	// only observations are recorded here.
	fmt.Println("\n=== Step 8: Record Wholesale Energy Prices ===")
	if err := recordWholesalePrices(tCtx, conn); err != nil {
		return fmt.Errorf("record wholesale prices: %w", err)
	}

	fmt.Println("\n=== Demo Seed Complete ===")
	fmt.Printf("Tenant:       %s (slug: %s)\n", tenantID, tenantSlug)
	fmt.Printf("Manifest:     structural resources applied via ApplyManifest\n")
	fmt.Printf("GSPs:         %d grid supply points with KWH inventory accounts\n", len(gspKwhAccountIDs))
	fmt.Printf("Customers:    %d customers with GBP billing + KWH consumption accounts\n", len(customerPartyIDs))
	fmt.Printf("Double-entry: each KWH meter read DEBITs GSP inventory, CREDITs customer account\n")
	fmt.Printf("Market:       30 days of wholesale energy prices\n")
	return nil
}

// ─── Tenant Creation ─────────────────────────────────────────────────────────

func createTenant(ctx context.Context, conn *grpc.ClientConn) error {
	client := tenantv1.NewTenantServiceClient(conn)

	resp, err := client.InitiateTenant(ctx, &tenantv1.InitiateTenantRequest{
		TenantId:        tenantID,
		DisplayName:     "Volterra Energy",
		SettlementAsset: "GBP",
		Subdomain:       tenantSlug + ".demo.meridianhub.cloud",
		Slug:            tenantSlug,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			fmt.Println("  Tenant already exists (idempotent).")
			return nil
		}
		return err
	}
	fmt.Printf("  Created tenant: %s\n", resp.GetTenant().GetTenantId())
	return nil
}

// ─── Manifest Application ────────────────────────────────────────────────────

// applyManifest loads the Volterra Energy demo manifest and submits it to
// ApplyManifestService. This provisions all structural resources:
//   - Instruments (GBP, KWH, CARBON_CREDIT)
//   - Account types (ENERGY_TRADING, INVENTORY_KWH, CARBON_INVENTORY, SETTLEMENT)
//   - Market data source (SEED_DEMO) and dataset (WHOLESALE_ENERGY_GBP_KWH)
//   - Valuation rules (KWH→GBP, CARBON_CREDIT→GBP)
//   - Organizations (UK Power Networks DNO, 4 Grid Supply Points)
//   - Internal accounts (4 GSP KWH inventory accounts)
//
// Re-running is idempotent: unchanged resources return NO_CHANGE status.
func applyManifest(ctx context.Context, conn *grpc.ClientConn) error {
	data, err := os.ReadFile("examples/manifests/volterra-energy-demo.json")
	if err != nil {
		// Try path relative to binary location (container deployments).
		data, err = os.ReadFile("/app/examples/manifests/volterra-energy-demo.json")
		if err != nil {
			return fmt.Errorf("read manifest: %w (tried ./examples/manifests/volterra-energy-demo.json and /app/examples/manifests/volterra-energy-demo.json)", err)
		}
	}

	var manifest controlplanev1.Manifest
	if err := protojson.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	client := controlplanev1.NewApplyManifestServiceClient(conn)

	resp, err := client.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  &manifest,
		DryRun:    false,
		AppliedBy: "seed-demo",
	})
	if err != nil {
		return fmt.Errorf("ApplyManifest: %w", err)
	}

	fmt.Printf("  Manifest applied (job: %s, status: %s)\n", resp.GetJobId(), resp.GetStatus().String())
	if diff := resp.GetDiffSummary(); diff != "" {
		fmt.Printf("  Changes: %s\n", diff)
	}
	if resp.GetStatus() == controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED {
		for _, ve := range resp.GetValidationErrors() {
			fmt.Printf("  VALIDATION ERROR: [%s] %s — %s\n", ve.GetCode(), ve.GetPath(), ve.GetMessage())
		}
		return fmt.Errorf("%w: %d errors", errManifestValidationFailed, len(resp.GetValidationErrors()))
	}
	return nil
}

// ─── GSP Internal Account Resolution ─────────────────────────────────────────

// gspAccountCodes maps GSP region codes to their manifest-defined internal account codes.
// These codes must match the internalAccounts[].code values in volterra-energy-demo.json.
var gspAccountCodes = []struct {
	region      string
	accountCode string
}{
	{region: "SEEB", accountCode: "GSP_KWH_SEEB"},
	{region: "EELC", accountCode: "GSP_KWH_EELC"},
	{region: "LOND", accountCode: "GSP_KWH_LOND"},
	{region: "SOUT", accountCode: "GSP_KWH_SOUT"},
}

// resolveGSPInternalAccountIDs looks up the account IDs for the GSP KWH inventory
// accounts provisioned by ApplyManifest. Returns IDs in GSP region order
// (SEEB, EELC, LOND, SOUT) matching the gspAccountCodes slice used by createAccounts.
func resolveGSPInternalAccountIDs(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	client := internalaccountv1.NewInternalAccountServiceClient(conn)

	accountIDs := make([]string, len(gspAccountCodes))
	for i, gsp := range gspAccountCodes {
		accountID, err := findInternalAccountByCode(ctx, client, gsp.accountCode)
		if err != nil {
			return nil, fmt.Errorf("resolve GSP KWH account %s (%s): %w", gsp.accountCode, gsp.region, err)
		}
		accountIDs[i] = accountID
		fmt.Printf("  GSP-KWH-%s: %s (account_code=%s)\n", gsp.region, accountID, gsp.accountCode)
	}

	return accountIDs, nil
}

func findInternalAccountByCode(ctx context.Context, client internalaccountv1.InternalAccountServiceClient, accountCode string) (string, error) {
	var pageToken string
	for {
		listResp, err := client.ListInternalAccounts(ctx, &internalaccountv1.ListInternalAccountsRequest{
			Pagination: &commonv1.Pagination{PageSize: 100, PageToken: pageToken},
		})
		if err != nil {
			return "", fmt.Errorf("list internal accounts to find %q: %w", accountCode, err)
		}
		for _, a := range listResp.GetFacilities() {
			if a.GetAccountCode() == accountCode {
				return a.GetAccountId(), nil
			}
		}
		pageToken = listResp.GetPagination().GetNextPageToken()
		if pageToken == "" {
			break
		}
	}
	return "", fmt.Errorf("%w: account_code=%q", errAccountNotFoundInListing, accountCode)
}

// ─── DNO Party Resolution ─────────────────────────────────────────────────────

// resolveOrganizationPartyID finds the party ID for an organization provisioned by
// ApplyManifest, identified by external reference. Paginates through all parties
// to ensure the organization is found even in tenants with many parties.
func resolveOrganizationPartyID(ctx context.Context, conn *grpc.ClientConn, externalRef string) (string, error) {
	client := partyv1.NewPartyServiceClient(conn)
	var pageToken string
	for {
		listResp, err := client.ListParties(ctx, &partyv1.ListPartiesRequest{
			PageSize:  100,
			PageToken: pageToken,
		})
		if err != nil {
			return "", fmt.Errorf("list parties to find %q: %w", externalRef, err)
		}
		for _, p := range listResp.GetParties() {
			if p.GetExternalReference() == externalRef {
				return p.GetPartyId(), nil
			}
		}
		pageToken = listResp.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}
	return "", fmt.Errorf("%w: external_reference=%q", errPartyNotFoundInListing, externalRef)
}

// ─── Customer Party Registration ─────────────────────────────────────────────

type customerInfo struct {
	legalName string
	gspIndex  int // index into gspAccountCodes (0=SEEB, 1=EELC, 2=LOND, 3=SOUT)
}

var customerDefinitions = []customerInfo{
	{legalName: "James Mitchell", gspIndex: 0},
	{legalName: "Sarah Thompson", gspIndex: 0},
	{legalName: "David Williams", gspIndex: 1},
	{legalName: "Emma Richardson", gspIndex: 1},
	{legalName: "Michael Clarke", gspIndex: 2},
	{legalName: "Rebecca Foster", gspIndex: 2},
	{legalName: "Andrew Patel", gspIndex: 2},
	{legalName: "Charlotte Davies", gspIndex: 3},
	{legalName: "Thomas Brown", gspIndex: 3},
	{legalName: "Olivia Hughes", gspIndex: 3},
}

func registerCustomerParties(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	client := partyv1.NewPartyServiceClient(conn)

	customerPartyIDs := make([]string, len(customerDefinitions))
	for i, cust := range customerDefinitions {
		partyID, err := registerParty(ctx, client, &partyv1.RegisterPartyRequest{
			PartyType:             partyv1.PartyType_PARTY_TYPE_PERSON,
			LegalName:             cust.legalName,
			DisplayName:           cust.legalName,
			ExternalReference:     fmt.Sprintf("CUST%03d", i+1),
			ExternalReferenceType: partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID,
		})
		if err != nil {
			return nil, fmt.Errorf("register customer %s: %w", cust.legalName, err)
		}
		customerPartyIDs[i] = partyID
		gspRegion := gspAccountCodes[cust.gspIndex].region
		fmt.Printf("  Customer: %s (%s, GSP: %s)\n", partyID, cust.legalName, gspRegion)
	}

	return customerPartyIDs, nil
}

func registerParty(ctx context.Context, client partyv1.PartyServiceClient, req *partyv1.RegisterPartyRequest) (string, error) {
	resp, err := client.RegisterParty(ctx, req)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return findPartyByExternalRef(ctx, client, req.GetExternalReference())
		}
		return "", err
	}
	return resp.GetParty().GetPartyId(), nil
}

func findPartyByExternalRef(ctx context.Context, client partyv1.PartyServiceClient, extRef string) (string, error) {
	listResp, err := client.ListParties(ctx, &partyv1.ListPartiesRequest{PageSize: 100})
	if err != nil {
		return "", fmt.Errorf("list parties to find existing %q: %w", extRef, err)
	}
	for _, p := range listResp.GetParties() {
		if p.GetExternalReference() == extRef {
			return p.GetPartyId(), nil
		}
	}
	return "", fmt.Errorf("%w: external_reference=%q", errPartyNotFoundInListing, extRef)
}

// ─── Account Creation ────────────────────────────────────────────────────────

type customerAccountPair struct {
	customerName    string
	partyID         string
	gbpAccountID    string
	kwhAccountID    string
	gspRegion       string
	gspKwhAccountID string // GSP internal account for the debit side of KWH double-entry
}

func createAccounts(ctx context.Context, conn *grpc.ClientConn, dnoPartyID string, customerPartyIDs []string, gspKwhAccountIDs []string) ([]customerAccountPair, error) {
	client := currentaccountv1.NewCurrentAccountServiceClient(conn)

	accounts := make([]customerAccountPair, len(customerPartyIDs))
	for i, partyID := range customerPartyIDs {
		cust := customerDefinitions[i]
		accounts[i].customerName = cust.legalName
		accounts[i].partyID = partyID
		accounts[i].gspRegion = gspAccountCodes[cust.gspIndex].region
		accounts[i].gspKwhAccountID = gspKwhAccountIDs[cust.gspIndex]

		// GBP billing account — charges in pounds sterling
		gbpID, err := createAccountIdempotent(ctx, client, partyID, fmt.Sprintf("VE-GBP-%03d", i+1), "GBP", dnoPartyID)
		if err != nil {
			return nil, fmt.Errorf("create GBP account for %s: %w", cust.legalName, err)
		}
		accounts[i].gbpAccountID = gbpID
		fmt.Printf("  GBP: %s (%s)\n", gbpID, cust.legalName)

		// KWH consumption tracking account — meter reading credits (ENERGY dimension)
		kwhID, err := createAccountIdempotent(ctx, client, partyID, fmt.Sprintf("VE-KWH-%03d", i+1), "KWH", dnoPartyID)
		if err != nil {
			return nil, fmt.Errorf("create KWH account for %s: %w", cust.legalName, err)
		}
		accounts[i].kwhAccountID = kwhID
		fmt.Printf("  KWH: %s (%s, GSP: %s)\n", kwhID, cust.legalName, accounts[i].gspRegion)
	}

	return accounts, nil
}

func createAccountIdempotent(ctx context.Context, client currentaccountv1.CurrentAccountServiceClient, partyID, extID, instrument, orgPartyID string) (string, error) {
	// Check if account already exists first (idempotent).
	// Only suppress "not found" errors — propagate real failures.
	existingID, err := findAccountByExternalID(ctx, client, extID)
	if err == nil && existingID != "" {
		return existingID, nil
	}
	if err != nil && !errors.Is(err, errAccountNotFoundInListing) {
		return "", fmt.Errorf("lookup existing account %s: %w", extID, err)
	}

	resp, err := client.InitiateCurrentAccount(ctx, &currentaccountv1.InitiateCurrentAccountRequest{
		PartyId:            partyID,
		ExternalIdentifier: extID,
		InstrumentCode:     instrument,
		OrgPartyId:         orgPartyID,
		ProductTypeCode:    "ENERGY_TRADING",
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return findAccountByExternalID(ctx, client, extID)
		}
		return "", err
	}
	return resp.GetAccountId(), nil
}

func findAccountByExternalID(ctx context.Context, client currentaccountv1.CurrentAccountServiceClient, extID string) (string, error) {
	listResp, err := client.ListCurrentAccounts(ctx, &currentaccountv1.ListCurrentAccountsRequest{
		PageSize: 100,
		Iban:     extID,
	})
	if err != nil {
		return "", fmt.Errorf("list accounts to find existing %q: %w", extID, err)
	}
	for _, a := range listResp.GetAccounts() {
		if a.GetExternalIdentifier() == extID {
			return a.GetAccountId(), nil
		}
	}
	return "", fmt.Errorf("%w: external_id=%q", errAccountNotFoundInListing, extID)
}

// ─── Balance Seeding ─────────────────────────────────────────────────────────

func seedBalances(ctx context.Context, conn *grpc.ClientConn, accounts []customerAccountPair) error {
	client := currentaccountv1.NewCurrentAccountServiceClient(conn)

	// Seed monthly consumption and billing data for each customer.
	// Typical UK residential usage: 8-12 kWh/day, fixed rate tariff of 24.5p/kWh.
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic seed for reproducibility

	for _, acct := range accounts {
		baseDailyKWH := 8.0 + rng.Float64()*4.0
		if err := seedCustomerBalances(ctx, client, acct, baseDailyKWH, rng); err != nil {
			return err
		}
	}

	return nil
}

func seedCustomerBalances(ctx context.Context, client currentaccountv1.CurrentAccountServiceClient, acct customerAccountPair, baseDailyKWH float64, rng *rand.Rand) error {
	const fixedRate = 0.245 // 24.5p/kWh fixed tariff

	now := time.Now().UTC()
	totalKWH := 0.0
	totalGBP := 0.0

	for day := 30; day >= 1; day-- {
		date := now.AddDate(0, 0, -day)
		dailyKWH := baseDailyKWH * (0.8 + rng.Float64()*0.4)
		dailyGBP := dailyKWH * fixedRate

		totalKWH += dailyKWH
		totalGBP += dailyGBP

		// GBP billing deposit — charge for energy consumed at fixed retail tariff
		if err := depositIdempotent(ctx, client, acct.gbpAccountID, dailyGBP, "GBP",
			fmt.Sprintf("Energy billing %s: %.2f kWh @ %.1fp/kWh", date.Format("2006-01-02"), dailyKWH, fixedRate*100),
			fmt.Sprintf("BILL-%s-%s", acct.partyID, date.Format("20060102")),
			"", // no clearing override for GBP — uses default clearing account
		); err != nil {
			return fmt.Errorf("deposit GBP for %s day %d: %w", acct.customerName, day, err)
		}

		// KWH meter read deposit — CREDIT customer kWh account, DEBIT GSP inventory account
		if err := depositIdempotent(ctx, client, acct.kwhAccountID, dailyKWH, "KWH",
			fmt.Sprintf("Meter read %s: %.2f kWh", date.Format("2006-01-02"), dailyKWH),
			fmt.Sprintf("METER-%s-%s", acct.partyID, date.Format("20060102")),
			acct.gspKwhAccountID, // GSP inventory account is the debit (clearing) side
		); err != nil {
			return fmt.Errorf("deposit KWH for %s day %d: %w", acct.customerName, day, err)
		}
	}

	fmt.Printf("  %s: %.1f kWh consumed, £%.2f billed (30 days)\n", acct.customerName, totalKWH, totalGBP)
	return nil
}

func depositIdempotent(ctx context.Context, client currentaccountv1.CurrentAccountServiceClient, accountID string, amount float64, currency, description, reference, clearingAccountID string) error {
	_, err := client.ExecuteDeposit(ctx, &currentaccountv1.ExecuteDepositRequest{
		AccountId:         accountID,
		Amount:            toMoney(amount, currency),
		Description:       description,
		Reference:         reference,
		ClearingAccountId: clearingAccountID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return nil
		}
		return err
	}
	return nil
}

// ─── Market Data Observations ─────────────────────────────────────────────────

// recordWholesalePrices seeds 30 days of synthetic wholesale energy prices into the
// WHOLESALE_ENERGY_GBP_KWH dataset provisioned by ApplyManifest.
// The data source (SEED_DEMO) and dataset are already registered by the manifest;
// this function only records observations (operational data).
func recordWholesalePrices(ctx context.Context, conn *grpc.ClientConn) error {
	client := marketv1.NewMarketInformationServiceClient(conn)

	const sourceCode = "SEED_DEMO"
	rng := rand.New(rand.NewSource(42)) //nolint:gosec
	now := time.Now().UTC()
	basePrice := 0.22 // 22p/kWh base wholesale price

	observationsRecorded := 0
	for day := 30; day >= 1; day-- {
		date := now.AddDate(0, 0, -day)
		dailyPrice := basePrice + (rng.Float64()-0.5)*0.10 // ±5p variation
		if dailyPrice < 0.12 {
			dailyPrice = 0.12 // floor
		}

		startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		endOfDay := startOfDay.Add(24 * time.Hour)

		_, err := client.RecordObservation(ctx, &marketv1.RecordObservationRequest{
			DatasetCode: "WHOLESALE_ENERGY_GBP_KWH",
			SourceCode:  sourceCode,
			ObservedAt:  timestamppb.New(startOfDay),
			ValidFrom:   timestamppb.New(startOfDay),
			ValidTo:     timestamppb.New(endOfDay),
			Value:       fmt.Sprintf("%.4f", dailyPrice),
			Quality:     marketv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		})
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
				continue
			}
			return fmt.Errorf("record observation day %d: %w", day, err)
		}
		observationsRecorded++
	}

	fmt.Printf("  Recorded %d wholesale energy price observations (WHOLESALE_ENERGY_GBP_KWH)\n", observationsRecorded)
	fmt.Printf("  Fixed retail tariff: 24.5p/kWh\n")
	fmt.Printf("  Wholesale range: ~15-27p/kWh (margin visible in account data)\n")

	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func withTenant(ctx context.Context) context.Context {
	md := metadata.Pairs("x-tenant-id", tenantID)
	return metadata.NewOutgoingContext(ctx, md)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// toMoney creates a MoneyAmount proto from a float amount and currency code.
// Uses google.type.Money (units + nanos) as required by the proto definition.
func toMoney(amount float64, currencyCode string) *commonv1.MoneyAmount {
	units := int64(amount)
	nanos := int32(math.Round((amount - float64(units)) * 1e9))
	return &commonv1.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: currencyCode,
			Units:        units,
			Nanos:        nanos,
		},
	}
}
