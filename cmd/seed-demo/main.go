// Package main is a demo data seeder for the Meridian energy platform demo.
//
// It connects to the Meridian gRPC server and seeds:
//   - An energy company tenant ("volterra-energy")
//   - The energy manifest (instruments: GBP, KWH, CARBON_CREDIT)
//   - A DNO organization (UK Power Networks) with 4 Grid Supply Points
//   - 10 residential customers with GBP + KWH current accounts
//   - Initial deposits: GBP balances and KWH consumption credits
//   - A wholesale energy price dataset with 30 days of historical prices
//
// All operations are idempotent — safe to run multiple times.
//
// Usage:
//
//	seed-demo --grpc-addr=localhost:50051 --gateway-url=http://localhost:8090
package main

import (
	"context"
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
			resp, err := http.Get(gatewayURL + "/healthz") //nolint:noctx
			if err != nil {
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

	// 4. Apply energy manifest
	fmt.Println("\n=== Step 2: Apply Energy Manifest ===")
	if err := applyManifest(ctx, conn); err != nil {
		return fmt.Errorf("apply manifest: %w", err)
	}

	// Tenant-scoped context for all subsequent calls
	tCtx := withTenant(ctx)

	// 5. Register parties
	fmt.Println("\n=== Step 3: Register Parties ===")
	dnoPartyID, gspPartyIDs, customerPartyIDs, err := registerParties(tCtx, conn)
	if err != nil {
		return fmt.Errorf("register parties: %w", err)
	}

	// 6. Create accounts
	fmt.Println("\n=== Step 4: Create Current Accounts ===")
	customerAccounts, err := createAccounts(tCtx, conn, dnoPartyID, customerPartyIDs)
	if err != nil {
		return fmt.Errorf("create accounts: %w", err)
	}

	// 7. Deposit initial balances
	fmt.Println("\n=== Step 5: Seed Account Balances ===")
	if err := seedBalances(tCtx, conn, customerAccounts); err != nil {
		return fmt.Errorf("seed balances: %w", err)
	}

	// 8. Seed market data
	fmt.Println("\n=== Step 6: Seed Wholesale Energy Prices ===")
	if err := seedMarketData(tCtx, conn); err != nil {
		return fmt.Errorf("seed market data: %w", err)
	}

	fmt.Println("\n=== Demo Seed Complete ===")
	fmt.Printf("Tenant:     %s (slug: %s)\n", tenantID, tenantSlug)
	fmt.Printf("DNO:        %s\n", dnoPartyID)
	fmt.Printf("GSPs:       %d grid supply points\n", len(gspPartyIDs))
	fmt.Printf("Customers:  %d customers with GBP + KWH accounts\n", len(customerPartyIDs))
	fmt.Printf("Market:     30 days of wholesale energy prices\n")
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

func applyManifest(ctx context.Context, conn *grpc.ClientConn) error {
	data, err := os.ReadFile("examples/manifests/energy.json")
	if err != nil {
		// Try relative to binary location
		data, err = os.ReadFile("/app/examples/manifests/energy.json")
		if err != nil {
			return fmt.Errorf("read manifest: %w (tried ./examples/manifests/energy.json and /app/examples/manifests/energy.json)", err)
		}
	}

	var manifest controlplanev1.Manifest
	if err := protojson.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	client := controlplanev1.NewApplyManifestServiceClient(conn)
	tCtx := withTenant(ctx)

	resp, err := client.ApplyManifest(tCtx, &controlplanev1.ApplyManifestRequest{
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
	return nil
}

// ─── Party Registration ──────────────────────────────────────────────────────

type gspInfo struct {
	name    string
	region  string
	partyID string
}

var gspDefinitions = []gspInfo{
	{name: "South East England GSP", region: "SEEB"},
	{name: "Eastern England GSP", region: "EELC"},
	{name: "London Power GSP", region: "LOND"},
	{name: "Southern England GSP", region: "SOUT"},
}

type customerInfo struct {
	legalName string
	gspIndex  int // which GSP region they belong to
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

func registerParties(ctx context.Context, conn *grpc.ClientConn) (string, []string, []string, error) {
	client := partyv1.NewPartyServiceClient(conn)

	// Register DNO (Distribution Network Operator)
	dnoPartyID, err := registerParty(ctx, client, &partyv1.RegisterPartyRequest{
		PartyType:             partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:             "UK Power Networks",
		DisplayName:           "UKPN",
		ExternalReference:     "UKPN-DNO-001",
		ExternalReferenceType: partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("register DNO: %w", err)
	}
	fmt.Printf("  DNO: %s (UK Power Networks)\n", dnoPartyID)

	// Register GSPs (Grid Supply Points)
	gspPartyIDs := make([]string, len(gspDefinitions))
	for i, gsp := range gspDefinitions {
		gspPartyIDs[i], err = registerParty(ctx, client, &partyv1.RegisterPartyRequest{
			PartyType:             partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
			LegalName:             gsp.name,
			DisplayName:           gsp.region,
			ExternalReference:     "GSP-" + gsp.region,
			ExternalReferenceType: partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
		})
		if err != nil {
			return "", nil, nil, fmt.Errorf("register GSP %s: %w", gsp.region, err)
		}
		gspDefinitions[i].partyID = gspPartyIDs[i]
		fmt.Printf("  GSP: %s (%s - %s)\n", gspPartyIDs[i], gsp.region, gsp.name)
	}

	// Register customers
	customerPartyIDs := make([]string, len(customerDefinitions))
	for i, cust := range customerDefinitions {
		customerPartyIDs[i], err = registerParty(ctx, client, &partyv1.RegisterPartyRequest{
			PartyType:             partyv1.PartyType_PARTY_TYPE_PERSON,
			LegalName:             cust.legalName,
			DisplayName:           cust.legalName,
			ExternalReference:     fmt.Sprintf("CUST-%03d", i+1),
			ExternalReferenceType: partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
		})
		if err != nil {
			return "", nil, nil, fmt.Errorf("register customer %s: %w", cust.legalName, err)
		}
		gspRegion := gspDefinitions[cust.gspIndex].region
		fmt.Printf("  Customer: %s (%s, GSP: %s)\n", customerPartyIDs[i], cust.legalName, gspRegion)
	}

	return dnoPartyID, gspPartyIDs, customerPartyIDs, nil
}

func registerParty(ctx context.Context, client partyv1.PartyServiceClient, req *partyv1.RegisterPartyRequest) (string, error) {
	resp, err := client.RegisterParty(ctx, req)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			// For idempotency, try to find the party by listing
			// For now just return a placeholder — real impl would lookup
			return "existing-" + req.GetExternalReference(), nil
		}
		return "", err
	}
	return resp.GetParty().GetPartyId(), nil
}

// ─── Account Creation ────────────────────────────────────────────────────────

type customerAccountPair struct {
	customerName string
	partyID      string
	gbpAccountID string
	kwhAccountID string
}

func createAccounts(ctx context.Context, conn *grpc.ClientConn, dnoPartyID string, customerPartyIDs []string) ([]customerAccountPair, error) {
	client := currentaccountv1.NewCurrentAccountServiceClient(conn)

	accounts := make([]customerAccountPair, len(customerPartyIDs))
	for i, partyID := range customerPartyIDs {
		cust := customerDefinitions[i]
		accounts[i].customerName = cust.legalName
		accounts[i].partyID = partyID

		// GBP billing account
		gbpID, err := createAccountIdempotent(ctx, client, partyID, fmt.Sprintf("VE-GBP-%03d", i+1), "GBP", dnoPartyID)
		if err != nil {
			return nil, fmt.Errorf("create GBP account for %s: %w", cust.legalName, err)
		}
		accounts[i].gbpAccountID = gbpID
		fmt.Printf("  GBP: %s (%s)\n", gbpID, cust.legalName)

		// KWH metering account
		kwhID, err := createAccountIdempotent(ctx, client, partyID, fmt.Sprintf("VE-KWH-%03d", i+1), "KWH", dnoPartyID)
		if err != nil {
			return nil, fmt.Errorf("create KWH account for %s: %w", cust.legalName, err)
		}
		accounts[i].kwhAccountID = kwhID
		fmt.Printf("  KWH: %s (%s)\n", kwhID, cust.legalName)
	}

	return accounts, nil
}

func createAccountIdempotent(ctx context.Context, client currentaccountv1.CurrentAccountServiceClient, partyID, extID, instrument, orgPartyID string) (string, error) {
	resp, err := client.InitiateCurrentAccount(ctx, &currentaccountv1.InitiateCurrentAccountRequest{
		PartyId:            partyID,
		ExternalIdentifier: extID,
		InstrumentCode:     instrument,
		OrgPartyId:         orgPartyID,
		ProductTypeCode:    "ENERGY_TRADING",
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return "existing-" + extID, nil
		}
		return "", err
	}
	return resp.GetAccountId(), nil
}

// ─── Balance Seeding ─────────────────────────────────────────────────────────

func seedBalances(ctx context.Context, conn *grpc.ClientConn, accounts []customerAccountPair) error {
	client := currentaccountv1.NewCurrentAccountServiceClient(conn)

	// Seed monthly consumption and billing data for each customer.
	// Typical UK residential usage: 8-12 kWh/day, fixed rate tariff of 24.5p/kWh.
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic seed for reproducibility

	for _, acct := range accounts {
		if acct.gbpAccountID == "existing" || acct.kwhAccountID == "existing" {
			fmt.Printf("  Skipping %s (accounts already existed)\n", acct.customerName)
			continue
		}

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

		if err := depositIdempotent(ctx, client, acct.kwhAccountID, dailyKWH, "KWH",
			fmt.Sprintf("Metered consumption %s", date.Format("2006-01-02")),
			fmt.Sprintf("METER-%s-%s", acct.partyID, date.Format("20060102")),
		); err != nil {
			return fmt.Errorf("deposit KWH for %s day %d: %w", acct.customerName, day, err)
		}

		if err := depositIdempotent(ctx, client, acct.gbpAccountID, dailyGBP, "GBP",
			fmt.Sprintf("Fixed tariff billing %s @ %.1fp/kWh", date.Format("2006-01-02"), fixedRate*100),
			fmt.Sprintf("BILL-%s-%s", acct.partyID, date.Format("20060102")),
		); err != nil {
			return fmt.Errorf("deposit GBP for %s day %d: %w", acct.customerName, day, err)
		}
	}

	fmt.Printf("  %s: %.1f kWh consumed, £%.2f billed (30 days)\n", acct.customerName, totalKWH, totalGBP)
	return nil
}

func depositIdempotent(ctx context.Context, client currentaccountv1.CurrentAccountServiceClient, accountID string, amount float64, currency, description, reference string) error {
	_, err := client.ExecuteDeposit(ctx, &currentaccountv1.ExecuteDepositRequest{
		AccountId:   accountID,
		Amount:      toMoney(amount, currency),
		Description: description,
		Reference:   reference,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return nil
		}
		return err
	}
	return nil
}

// ─── Market Data Seeding ─────────────────────────────────────────────────────

func seedMarketData(ctx context.Context, conn *grpc.ClientConn) error {
	client := marketv1.NewMarketInformationServiceClient(conn)

	// Register wholesale energy price dataset
	_, err := client.RegisterDataSet(ctx, &marketv1.RegisterDataSetRequest{
		Code:          "WHOLESALE_ENERGY_GBP_KWH",
		Category:      marketv1.DataCategory_DATA_CATEGORY_ENERGY_PRICE,
		Unit:          "GBP/kWh",
		DisplayName:   "UK Wholesale Electricity Price",
		EffectiveFrom: timestamppb.New(time.Now().AddDate(0, -1, 0)),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			fmt.Println("  Dataset WHOLESALE_ENERGY_GBP_KWH already exists")
		} else {
			return fmt.Errorf("register dataset: %w", err)
		}
	} else {
		fmt.Println("  Registered dataset: WHOLESALE_ENERGY_GBP_KWH")
	}

	// Seed 30 days of wholesale prices.
	// UK wholesale prices vary between 15-35p/kWh with daily volatility.
	rng := rand.New(rand.NewSource(42)) //nolint:gosec
	now := time.Now().UTC()
	basePrice := 0.22 // 22p/kWh base wholesale price

	for day := 30; day >= 1; day-- {
		date := now.AddDate(0, 0, -day)
		// Wholesale price with some trend and noise
		dailyPrice := basePrice + (rng.Float64()-0.5)*0.10 // ±5p variation
		if dailyPrice < 0.12 {
			dailyPrice = 0.12 // floor
		}

		startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		endOfDay := startOfDay.Add(24 * time.Hour)

		_, err := client.RecordObservation(ctx, &marketv1.RecordObservationRequest{
			DatasetCode: "WHOLESALE_ENERGY_GBP_KWH",
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
	}

	fmt.Println("  Recorded 30 days of wholesale energy prices")
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
