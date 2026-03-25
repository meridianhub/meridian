// Package cmd implements the seed-dev CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	marketv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Sentinel errors for idempotent fixture lookups.
var (
	errPartyNotFoundInListing   = fmt.Errorf("party reported as existing but not found in listing")
	errAccountNotFoundInListing = fmt.Errorf("account reported as existing but not found in listing")
)

// ─── Fixture Data Definitions ────────────────────────────────────────────────

// gspAccountCodes maps GSP region codes to their manifest-defined internal account codes.
// These codes must match the internalAccounts[].code values in the manifest JSON.
var gspAccountCodes = []struct {
	region      string
	accountCode string
}{
	{region: "SEEB", accountCode: "GSP_KWH_SEEB"},
	{region: "EELC", accountCode: "GSP_KWH_EELC"},
	{region: "LOND", accountCode: "GSP_KWH_LOND"},
	{region: "SOUT", accountCode: "GSP_KWH_SOUT"},
}

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

type customerAccountPair struct {
	customerName    string
	partyID         string
	gbpAccountID    string
	kwhAccountID    string
	gspRegion       string
	gspKwhAccountID string // GSP internal account for the debit side of KWH double-entry
}

// ─── Fixture Entry Point ─────────────────────────────────────────────────────

// runFixtures seeds operational demo data (customers, accounts, meter reads,
// wholesale prices) via gRPC API calls. All operations are idempotent.
func runFixtures(ctx context.Context, conn *grpc.ClientConn, tid string) error {
	tCtx := withTenantCtx(ctx, tid)

	// 1. Resolve GSP KWH internal account IDs provisioned by the manifest.
	fmt.Println("\n--- Resolve GSP Internal Account IDs ---")
	gspKwhAccountIDs, err := resolveGSPInternalAccountIDs(tCtx, conn)
	if err != nil {
		return fmt.Errorf("resolve GSP internal accounts: %w", err)
	}

	// 2. Register customer parties.
	fmt.Println("\n--- Register Customer Parties ---")
	customerPartyIDs, err := registerCustomerParties(tCtx, conn)
	if err != nil {
		return fmt.Errorf("register customer parties: %w", err)
	}

	// 3. Resolve the DNO party ID for the manifest-provisioned UKPN organization.
	fmt.Println("\n--- Resolve DNO Party ID ---")
	dnoPartyID, err := resolveOrganizationPartyID(tCtx, conn, "UKPNDNO001")
	if err != nil {
		return fmt.Errorf("resolve DNO party ID: %w", err)
	}
	fmt.Printf("  DNO party ID: %s\n", dnoPartyID)

	// 4. Create customer current accounts.
	fmt.Println("\n--- Create Customer Accounts ---")
	customerAccounts, err := createCustomerAccounts(tCtx, conn, dnoPartyID, customerPartyIDs, gspKwhAccountIDs)
	if err != nil {
		return fmt.Errorf("create accounts: %w", err)
	}

	// 5. Register party associations (customer->DNO, customer->GSP, DNO->GSP).
	fmt.Println("\n--- Register Party Associations ---")
	gspPartyIDs, err := resolveGSPPartyIDs(tCtx, conn)
	if err != nil {
		return fmt.Errorf("resolve GSP party IDs: %w", err)
	}
	if err := registerPartyAssociations(tCtx, conn, dnoPartyID, customerPartyIDs, gspPartyIDs); err != nil {
		return fmt.Errorf("register party associations: %w", err)
	}

	// 6. Deposit meter reads and billing amounts.
	fmt.Println("\n--- Seed Account Balances ---")
	if err := seedBalances(tCtx, conn, customerAccounts); err != nil {
		return fmt.Errorf("seed balances: %w", err)
	}

	// 7. Record wholesale energy price observations.
	fmt.Println("\n--- Record Wholesale Energy Prices ---")
	if err := recordWholesalePrices(tCtx, conn); err != nil {
		return fmt.Errorf("record wholesale prices: %w", err)
	}

	fmt.Println("\n=== Fixture Seed Complete ===")
	fmt.Printf("  GSPs:         %d grid supply points with KWH inventory accounts\n", len(gspKwhAccountIDs))
	fmt.Printf("  Customers:    %d customers with GBP billing + KWH consumption accounts\n", len(customerPartyIDs))
	fmt.Printf("  Associations: %d party associations (customer->DNO, customer->GSP, DNO->GSP)\n",
		len(customerPartyIDs)*2+len(gspPartyIDs))
	fmt.Printf("  Double-entry: each KWH meter read DEBITs GSP inventory, CREDITs customer account\n")
	fmt.Printf("  Market:       30 days of wholesale energy prices\n")
	return nil
}

// ─── Tenant Context ──────────────────────────────────────────────────────────

func withTenantCtx(ctx context.Context, tid string) context.Context {
	md := metadata.Pairs("x-tenant-id", tid)
	return metadata.NewOutgoingContext(ctx, md)
}

// ─── GSP Internal Account Resolution ─────────────────────────────────────────

// resolveGSPInternalAccountIDs looks up the account IDs for the GSP KWH inventory
// accounts provisioned by ApplyManifest. Returns IDs in GSP region order
// (SEEB, EELC, LOND, SOUT) matching the gspAccountCodes slice used by createCustomerAccounts.
func resolveGSPInternalAccountIDs(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	client := internalaccountv1.NewInternalAccountServiceClient(conn)

	accountIDs := make([]string, len(gspAccountCodes))
	for i, gsp := range gspAccountCodes {
		accountID, err := findInternalAccountByCode(ctx, client, gsp.accountCode)
		if err != nil {
			return nil, fmt.Errorf("resolve GSP KWH account %s (%s): %w", gsp.accountCode, gsp.region, err)
		}
		// Strip "IBA-" prefix to get raw UUID for use as clearing_account_id.
		// The deposit orchestrator validates clearing_account_id as UUID.
		accountIDs[i] = strings.TrimPrefix(accountID, "IBA-")
		fmt.Printf("  GSP-KWH-%s: %s (account_code=%s)\n", gsp.region, accountIDs[i], gsp.accountCode)
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
// ApplyManifest, identified by external reference.
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

func createCustomerAccounts(ctx context.Context, conn *grpc.ClientConn, dnoPartyID string, customerPartyIDs []string, gspKwhAccountIDs []string) ([]customerAccountPair, error) {
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

		// KWH meter read deposit - CREDIT customer kWh account, DEBIT GSP inventory account.
		// Uses InstrumentAmount (input field) for non-monetary instruments.
		// NOTE: financial-accounting currently only supports ISO-4217 currencies,
		// so KWH deposits fail at the posting layer with InvalidArgument. Skip
		// that specific error gracefully; propagate unexpected errors (network, auth).
		if err := depositInstrumentIdempotent(ctx, client, acct.kwhAccountID, dailyKWH, "KWH",
			fmt.Sprintf("Meter read %s: %.2f kWh", date.Format("2006-01-02"), dailyKWH),
			fmt.Sprintf("METER-%s-%s", acct.partyID, date.Format("20060102")),
			acct.gspKwhAccountID, // GSP inventory account is the debit (clearing) side
		); err != nil {
			// The saga wraps the financial-accounting rejection as Internal, and the
			// inner error is InvalidArgument with "invalid currency". Match on the
			// error message to avoid masking unrelated Internal errors.
			errMsg := err.Error()
			if strings.Contains(errMsg, "invalid currency") || strings.Contains(errMsg, "invalid posting_amount") {
				if day == 30 {
					fmt.Printf("  [WARN] KWH deposits skipped for %s (financial-accounting does not yet support non-monetary instruments)\n", acct.customerName)
				}
			} else {
				return fmt.Errorf("deposit KWH for %s day %d: %w", acct.customerName, day, err)
			}
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

// depositInstrumentIdempotent deposits using the InstrumentAmount input field,
// which properly represents non-monetary instruments (KWH, CARBON_CREDIT, etc.)
// without abusing google.type.Money's CurrencyCode field.
func depositInstrumentIdempotent(ctx context.Context, client currentaccountv1.CurrentAccountServiceClient, accountID string, amount float64, instrumentCode, description, reference, clearingAccountID string) error {
	_, err := client.ExecuteDeposit(ctx, &currentaccountv1.ExecuteDepositRequest{
		AccountId:         accountID,
		Description:       description,
		Reference:         reference,
		ClearingAccountId: clearingAccountID,
		Input: &quantityv1.InstrumentAmount{
			Amount:         fmt.Sprintf("%.3f", amount),
			InstrumentCode: instrumentCode,
			Version:        1,
		},
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
func recordWholesalePrices(ctx context.Context, conn *grpc.ClientConn) error {
	client := marketv1.NewMarketInformationServiceClient(conn)

	const sourceCode = "SEED_DEMO"
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic seed for reproducible fixture data
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

// ─── Party Associations ──────────────────────────────────────────────────────

// gspExternalRefs maps GSP region codes to their manifest-defined external references.
var gspExternalRefs = []string{"GSPSEEB", "GSPEELC", "GSPLOND", "GSPSOUT"}

// resolveGSPPartyIDs finds the party IDs for all GSP organizations.
func resolveGSPPartyIDs(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	gspPartyIDs := make([]string, len(gspExternalRefs))
	for i, extRef := range gspExternalRefs {
		partyID, err := resolveOrganizationPartyID(ctx, conn, extRef)
		if err != nil {
			return nil, fmt.Errorf("resolve GSP party %s: %w", extRef, err)
		}
		gspPartyIDs[i] = partyID
	}
	return gspPartyIDs, nil
}

// registerPartyAssociations creates associations between customers, the DNO, and GSPs.
// - Each customer -> DNO (BUSINESS_PARTNER: electricity customer)
// - Each customer -> their GSP (BUSINESS_PARTNER: grid supply point customer)
// - DNO -> each GSP (BUSINESS_PARTNER: operates grid supply point)
func registerPartyAssociations(ctx context.Context, conn *grpc.ClientConn, dnoPartyID string, customerPartyIDs, gspPartyIDs []string) error {
	client := partyv1.NewPartyServiceClient(conn)

	for i, custPartyID := range customerPartyIDs {
		cust := customerDefinitions[i]
		gspPartyID := gspPartyIDs[cust.gspIndex]
		gspRegion := gspAccountCodes[cust.gspIndex].region

		// Customer -> DNO association
		if err := registerAssociationIdempotent(ctx, client, custPartyID, dnoPartyID,
			partyv1.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER); err != nil {
			return fmt.Errorf("associate customer %s with DNO: %w", cust.legalName, err)
		}

		// Customer -> GSP association
		if err := registerAssociationIdempotent(ctx, client, custPartyID, gspPartyID,
			partyv1.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER); err != nil {
			return fmt.Errorf("associate customer %s with GSP %s: %w", cust.legalName, gspRegion, err)
		}
	}

	// DNO -> each GSP association
	for i, gspPartyID := range gspPartyIDs {
		if err := registerAssociationIdempotent(ctx, client, dnoPartyID, gspPartyID,
			partyv1.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER); err != nil {
			return fmt.Errorf("associate DNO with GSP %s: %w", gspExternalRefs[i], err)
		}
	}

	fmt.Printf("  Registered %d associations (customer->DNO, customer->GSP, DNO->GSP)\n",
		len(customerPartyIDs)*2+len(gspPartyIDs))
	return nil
}

func registerAssociationIdempotent(ctx context.Context, client partyv1.PartyServiceClient, partyID, relatedPartyID string, relType partyv1.RelationshipType) error {
	_, err := client.RegisterAssociations(ctx, &partyv1.RegisterAssociationsRequest{
		PartyId:          partyID,
		RelatedPartyId:   relatedPartyID,
		RelationshipType: relType,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return nil
		}
		return err
	}
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// toMoney creates a MoneyAmount proto from a float amount and currency code.
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
