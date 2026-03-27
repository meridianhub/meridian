// Package cmd implements the seed-dev CLI commands.
package cmd

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	marketv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── PAYG Customer Definitions ──────────────────────────────────────────────

// paygCustomer represents a dual-fuel PAYG customer with valid meter references.
// MPANs use the Southern Electric (SEEB/SOUT) distribution area format:
//   - Import MPAN: 19 00 801 0123 456 (profile class 01 = domestic unrestricted)
//   - MPRN: 10-digit gas meter reference
type paygCustomer struct {
	legalName   string
	mpan        string  // Electricity import MPAN (Southern region)
	mprn        string  // Gas MPRN
	gspRegion   string  // GSP group for wholesale cost attribution
	dailyKwhAvg float64 // Average daily electricity kWh (determines First/Saver split)
}

// Sample PAYG customers in Southern England (SEEB/SOUT distribution areas).
// Mix of usage profiles to demonstrate block tariff behavior:
//   - Low use (3-5 kWh/day): mostly First Rate, higher margin per kWh
//   - Medium use (8-10 kWh/day): mixed First/Saver, typical household
//   - High use (15+ kWh/day): mostly Saver Rate, lower margin per kWh
var paygCustomers = []paygCustomer{
	{
		legalName:   "Margaret Thornton",
		mpan:        "1900801012345601",
		mprn:        "7613204501",
		gspRegion:   "SOUT",
		dailyKwhAvg: 3.5, // pensioner, low use - mostly First Rate
	},
	{
		legalName:   "James & Claire Okonkwo",
		mpan:        "1900801012345602",
		mprn:        "7613204502",
		gspRegion:   "SOUT",
		dailyKwhAvg: 9.0, // family of 4, medium use
	},
	{
		legalName:   "Ryan Cooper",
		mpan:        "1900801012345603",
		mprn:        "7613204503",
		gspRegion:   "SEEB",
		dailyKwhAvg: 5.0, // single occupant, moderate
	},
	{
		legalName:   "Priya Sharma",
		mpan:        "1900801012345604",
		mprn:        "7613204504",
		gspRegion:   "SEEB",
		dailyKwhAvg: 16.0, // home office + electric heating, high use
	},
	{
		legalName:   "David & Susan Whitfield",
		mpan:        "1900801012345605",
		mprn:        "7613204505",
		gspRegion:   "SOUT",
		dailyKwhAvg: 11.0, // family, electric vehicle charger
	},
}

// ─── PAYG Fixture Entry Point ───────────────────────────────────────────────

// runPaygFixtures seeds demo data for the PAYG energy tenant:
//   - 5 dual-fuel customers with valid MPANs/MPRNs
//   - 30 days of block tariff rates (First Rate, Saver Rate for elec + gas)
//   - 30 days of wholesale electricity + gas prices
//   - 30 days of GBP billing based on block tariff consumption
func runPaygFixtures(ctx context.Context, conn *grpc.ClientConn, tid string) error {
	tCtx := withTenantCtx(ctx, tid)

	// 1. Resolve the supplier organization party for account ownership.
	fmt.Println("\n--- Resolve Supplier Organization ---")
	supplierPartyID, err := resolveOrganizationPartyID(tCtx, conn, "GETW")
	if err != nil {
		return fmt.Errorf("resolve supplier party: %w", err)
	}
	fmt.Printf("  Supplier party ID: %s (MPID: GETW)\n", supplierPartyID)

	// 2. Register dual-fuel customers with valid MPANs.
	fmt.Println("\n--- Register PAYG Customers ---")
	customerPartyIDs, err := registerPaygCustomers(tCtx, conn)
	if err != nil {
		return fmt.Errorf("register customers: %w", err)
	}

	// 3. Resolve DNO and GSP parties, register party hierarchy.
	fmt.Println("\n--- Register Party Hierarchy (DNO -> GSP -> Customer) ---")
	dnoPartyID, err := resolveOrganizationPartyID(tCtx, conn, "SEPD")
	if err != nil {
		return fmt.Errorf("resolve DNO party: %w", err)
	}
	fmt.Printf("  DNO party ID: %s (SEPD)\n", dnoPartyID)

	gspExtRefs := []string{"GSPSOUT", "GSPSEEB", "GSPSWAE", "GSPWMID"}
	gspPartyIDs := make([]string, len(gspExtRefs))
	for i, ref := range gspExtRefs {
		gspPartyIDs[i], err = resolveOrganizationPartyID(tCtx, conn, ref)
		if err != nil {
			return fmt.Errorf("resolve GSP %s: %w", ref, err)
		}
	}

	// Register associations:
	//   - Customer -> Supplier (energy supply relationship)
	//   - Customer -> DNO (network customer)
	//   - DNO -> GSP (DNO operates GSP areas)
	// Note: GSP association is via the MPAN (meter location), not the customer.
	// A customer may have MPANs in multiple GSP areas. The kWh consumption
	// accounts carry the GSP context via their account metadata, enabling
	// aggregation queries like "consumption by GSP" without a direct
	// customer -> GSP party link.
	partyClient := partyv1.NewPartyServiceClient(conn)
	for _, custPartyID := range customerPartyIDs {
		_ = registerAssociationIdempotent(tCtx, partyClient, custPartyID, supplierPartyID, partyv1.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER)
		_ = registerAssociationIdempotent(tCtx, partyClient, custPartyID, dnoPartyID, partyv1.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER)
	}
	for _, gspPartyID := range gspPartyIDs {
		_ = registerAssociationIdempotent(tCtx, partyClient, dnoPartyID, gspPartyID, partyv1.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER)
	}
	fmt.Printf("  Registered: %d customer->supplier, %d customer->DNO, %d DNO->GSP associations\n",
		len(customerPartyIDs), len(customerPartyIDs), len(gspPartyIDs))

	// 4. Create prepayment and consumption accounts (GBP billing + kWh tracking per fuel).
	fmt.Println("\n--- Create Customer Accounts ---")
	if err := createPaygAccounts(tCtx, conn, supplierPartyID, customerPartyIDs); err != nil {
		return fmt.Errorf("create accounts: %w", err)
	}

	// 6. Seed block tariff rates into market data.
	fmt.Println("\n--- Seed Block Tariff Rates ---")
	if err := seedBlockTariffRates(tCtx, conn); err != nil {
		return fmt.Errorf("seed tariff rates: %w", err)
	}

	// 7. Seed wholesale energy prices.
	fmt.Println("\n--- Seed Wholesale Prices ---")
	if err := seedWholesalePrices(tCtx, conn); err != nil {
		return fmt.Errorf("seed wholesale prices: %w", err)
	}

	// 8. Seed consumption billing (GBP charges based on block tariff).
	fmt.Println("\n--- Seed Consumption Billing ---")
	if err := seedPaygBilling(tCtx, conn, customerPartyIDs); err != nil {
		return fmt.Errorf("seed billing: %w", err)
	}

	fmt.Println("\n=== PAYG Fixture Seed Complete ===")
	fmt.Printf("  Customers:  %d dual-fuel (MPAN + MPRN)\n", len(paygCustomers))
	fmt.Printf("  Tariffs:    First Rate / Saver Rate for electricity + gas (30 days)\n")
	fmt.Printf("  Wholesale:  Electricity + gas spot prices (30 days)\n")
	fmt.Printf("  Billing:    GBP charges with block tariff applied (30 days per customer)\n")
	return nil
}

// ─── Customer Registration ──────────────────────────────────────────────────

func registerPaygCustomers(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	client := partyv1.NewPartyServiceClient(conn)

	partyIDs := make([]string, len(paygCustomers))
	for i, cust := range paygCustomers {
		// Register using MPAN as external reference (primary meter identifier).
		partyID, err := registerParty(ctx, client, &partyv1.RegisterPartyRequest{
			PartyType:             partyv1.PartyType_PARTY_TYPE_PERSON,
			LegalName:             cust.legalName,
			DisplayName:           cust.legalName,
			ExternalReference:     cust.mpan,
			ExternalReferenceType: partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID,
		})
		if err != nil {
			return nil, fmt.Errorf("register %s: %w", cust.legalName, err)
		}
		partyIDs[i] = partyID
		fmt.Printf("  %s: MPAN=%s MPRN=%s GSP=%s avg=%.0fkWh/day\n",
			cust.legalName, cust.mpan, cust.mprn, cust.gspRegion, cust.dailyKwhAvg)
	}

	return partyIDs, nil
}

// ─── Account Creation ───────────────────────────────────────────────────────

func createPaygAccounts(ctx context.Context, conn *grpc.ClientConn, supplierPartyID string, customerPartyIDs []string) error {
	client := currentaccountv1.NewCurrentAccountServiceClient(conn)

	for i, partyID := range customerPartyIDs {
		cust := paygCustomers[i]

		// Electricity prepayment account (GBP)
		elecID, err := createAccountIdempotent(ctx, client, partyID,
			fmt.Sprintf("UT-ELEC-%s", cust.mpan), "GBP", supplierPartyID)
		if err != nil {
			return fmt.Errorf("create electricity account for %s: %w", cust.legalName, err)
		}
		fmt.Printf("  Elec: %s (%s, MPAN: %s)\n", elecID, cust.legalName, cust.mpan)

		// Gas prepayment account (GBP)
		gasID, err := createAccountIdempotent(ctx, client, partyID,
			fmt.Sprintf("UT-GAS-%s", cust.mprn), "GBP", supplierPartyID)
		if err != nil {
			return fmt.Errorf("create gas account for %s: %w", cust.legalName, err)
		}
		fmt.Printf("  Gas:  %s (%s, MPRN: %s)\n", gasID, cust.legalName, cust.mprn)

		// Electricity consumption tracking account (KWH_ELEC)
		elecKwhID, err := createAccountIdempotent(ctx, client, partyID,
			fmt.Sprintf("KWH-ELEC-%s", cust.mpan), "KWH_ELEC", supplierPartyID)
		if err != nil {
			return fmt.Errorf("create elec kWh account for %s: %w", cust.legalName, err)
		}
		fmt.Printf("  kWh(E): %s (%s)\n", elecKwhID, cust.legalName)

		// Gas consumption tracking account (KWH_GAS)
		gasKwhID, err := createAccountIdempotent(ctx, client, partyID,
			fmt.Sprintf("KWH-GAS-%s", cust.mprn), "KWH_GAS", supplierPartyID)
		if err != nil {
			return fmt.Errorf("create gas kWh account for %s: %w", cust.legalName, err)
		}
		fmt.Printf("  kWh(G): %s (%s)\n", gasKwhID, cust.legalName)
	}

	return nil
}

// ─── Block Tariff Rate Seeding ──────────────────────────────────────────────

// seedBlockTariffRates records 30 days of block tariff rates.
// Rates are VAT-inclusive as published (Oct 2025 cap period):
//   - Electricity First Rate: 51.85p/kWh (first 2 kWh/day)
//   - Electricity Saver Rate: 26.010p/kWh (above 2 kWh/day)
//   - Gas First Rate: 23.355p/kWh (first 2 kWh/day)
//   - Gas Saver Rate: 6.211p/kWh (above 2 kWh/day)
func seedBlockTariffRates(ctx context.Context, conn *grpc.ClientConn) error {
	client := marketv1.NewMarketInformationServiceClient(conn)

	const sourceCode = "RETAIL_TARIFF"

	tariffRates := []struct {
		datasetCode string
		value       string
		label       string
	}{
		{"PAYG_ELEC_FIRST_RATE", "0.5185", "Electricity First Rate: 51.85p/kWh"},
		{"PAYG_ELEC_SAVER_RATE", "0.26010", "Electricity Saver Rate: 26.010p/kWh"},
		{"PAYG_GAS_FIRST_RATE", "0.23355", "Gas First Rate: 23.355p/kWh"},
		{"PAYG_GAS_SAVER_RATE", "0.06211", "Gas Saver Rate: 6.211p/kWh"},
	}

	now := time.Now().UTC()
	totalRecorded := 0

	for _, tariff := range tariffRates {
		recorded := 0
		for day := 30; day >= 0; day-- {
			date := now.AddDate(0, 0, -day)
			startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
			endOfDay := startOfDay.Add(24 * time.Hour)

			_, err := client.RecordObservation(ctx, &marketv1.RecordObservationRequest{
				DatasetCode: tariff.datasetCode,
				SourceCode:  sourceCode,
				ObservedAt:  timestamppb.New(startOfDay),
				ValidFrom:   timestamppb.New(startOfDay),
				ValidTo:     timestamppb.New(endOfDay),
				Value:       tariff.value,
				Quality:     marketv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
			})
			if err != nil {
				if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
					continue
				}
				return fmt.Errorf("record %s day %d: %w", tariff.datasetCode, day, err)
			}
			recorded++
		}
		totalRecorded += recorded
		fmt.Printf("  %s: %d observations\n", tariff.label, recorded)
	}

	fmt.Printf("  Total: %d tariff rate observations across 4 datasets\n", totalRecorded)
	return nil
}

// ─── Wholesale Price Seeding ────────────────────────────────────────────────

// seedWholesalePrices records 30 days of wholesale electricity and gas prices.
// Prices are synthetic but realistic for UK wholesale markets:
//   - Electricity: base 8.5p/kWh with ±3p daily variation (seasonal + volatility)
//   - Gas: base 3.2p/kWh with ±1.5p daily variation
func seedWholesalePrices(ctx context.Context, conn *grpc.ClientConn) error {
	client := marketv1.NewMarketInformationServiceClient(conn)

	const sourceCode = "WHOLESALE_MARKET"
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic for reproducibility
	now := time.Now().UTC()

	wholesaleDatasets := []struct {
		datasetCode string
		basePrice   float64
		variation   float64
		floor       float64
		label       string
	}{
		{"WHOLESALE_ELEC_GBP_KWH", 0.085, 0.03, 0.04, "Wholesale Electricity"},
		{"WHOLESALE_GAS_GBP_KWH", 0.032, 0.015, 0.015, "Wholesale Gas"},
	}

	for _, ds := range wholesaleDatasets {
		recorded := 0
		for day := 30; day >= 0; day-- {
			date := now.AddDate(0, 0, -day)
			startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
			endOfDay := startOfDay.Add(24 * time.Hour)

			// Add seasonal component (higher in winter months)
			seasonalFactor := 1.0 + 0.15*math.Cos(2*math.Pi*float64(date.YearDay()-15)/365)
			dailyPrice := ds.basePrice*seasonalFactor + (rng.Float64()-0.5)*2*ds.variation
			if dailyPrice < ds.floor {
				dailyPrice = ds.floor
			}

			_, err := client.RecordObservation(ctx, &marketv1.RecordObservationRequest{
				DatasetCode: ds.datasetCode,
				SourceCode:  sourceCode,
				ObservedAt:  timestamppb.New(startOfDay),
				ValidFrom:   timestamppb.New(startOfDay),
				ValidTo:     timestamppb.New(endOfDay),
				Value:       fmt.Sprintf("%.5f", dailyPrice),
				Quality:     marketv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
			})
			if err != nil {
				if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
					continue
				}
				return fmt.Errorf("record %s day %d: %w", ds.datasetCode, day, err)
			}
			recorded++
		}
		fmt.Printf("  %s: %d daily observations\n", ds.label, recorded)
	}

	return nil
}

// ─── Consumption Billing Seeding ────────────────────────────────────────────

// seedPaygBilling seeds 30 days of GBP billing for each customer.
// Applies the block tariff logic: first 2 kWh at First Rate, remainder at Saver Rate.
// Gas usage is ~60% of electricity kWh for a typical dual-fuel household.
func seedPaygBilling(ctx context.Context, conn *grpc.ClientConn, customerPartyIDs []string) error {
	client := currentaccountv1.NewCurrentAccountServiceClient(conn)
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic

	// Block tariff rates (ex-VAT for billing, since prepayment balance is net-of-VAT)
	const (
		elecFirstRate = 0.49381 // 51.85p / 1.05 = 49.381p ex-VAT
		elecSaverRate = 0.24771 // 26.010p / 1.05 = 24.771p ex-VAT
		gasFirstRate  = 0.22243 // 23.355p / 1.05 = 22.243p ex-VAT
		gasSaverRate  = 0.05915 // 6.211p / 1.05 = 5.915p ex-VAT
		threshold     = 2.0     // kWh daily block threshold
	)

	now := time.Now().UTC()

	for i, partyID := range customerPartyIDs {
		cust := paygCustomers[i]
		totalElecGBP := 0.0
		totalGasGBP := 0.0

		// Find the customer's electricity account ID
		elecAccountID, err := findAccountByExternalID(ctx, client, fmt.Sprintf("UT-ELEC-%s", cust.mpan))
		if err != nil {
			return fmt.Errorf("find elec account for %s: %w", cust.legalName, err)
		}
		gasAccountID, err := findAccountByExternalID(ctx, client, fmt.Sprintf("UT-GAS-%s", cust.mprn))
		if err != nil {
			return fmt.Errorf("find gas account for %s: %w", cust.legalName, err)
		}

		for day := 30; day >= 1; day-- {
			date := now.AddDate(0, 0, -day)
			dailyVariation := 0.7 + rng.Float64()*0.6 // 70-130% of average

			// Electricity consumption with block tariff
			elecKwh := cust.dailyKwhAvg * dailyVariation
			elecFirstKwh := math.Min(elecKwh, threshold)
			elecSaverKwh := math.Max(elecKwh-threshold, 0)
			elecCharge := elecFirstKwh*elecFirstRate + elecSaverKwh*elecSaverRate
			totalElecGBP += elecCharge

			ref := fmt.Sprintf("ELEC-%s-%s", partyID, date.Format("20060102"))
			desc := fmt.Sprintf("Electricity %s: %.1fkWh (%.1f@first + %.1f@saver) = £%.2f",
				date.Format("2006-01-02"), elecKwh, elecFirstKwh, elecSaverKwh, elecCharge)

			if err := depositIdempotent(ctx, client, elecAccountID, elecCharge, "GBP", desc, ref, ""); err != nil {
				return fmt.Errorf("deposit elec for %s day %d: %w", cust.legalName, day, err)
			}

			// Gas consumption with block tariff (~60% of elec kWh, different rates)
			gasKwh := elecKwh * (0.5 + rng.Float64()*0.3) // 50-80% of elec
			gasFirstKwh := math.Min(gasKwh, threshold)
			gasSaverKwh := math.Max(gasKwh-threshold, 0)
			gasCharge := gasFirstKwh*gasFirstRate + gasSaverKwh*gasSaverRate
			totalGasGBP += gasCharge

			gasRef := fmt.Sprintf("GAS-%s-%s", partyID, date.Format("20060102"))
			gasDesc := fmt.Sprintf("Gas %s: %.1fkWh (%.1f@first + %.1f@saver) = £%.2f",
				date.Format("2006-01-02"), gasKwh, gasFirstKwh, gasSaverKwh, gasCharge)

			if err := depositIdempotent(ctx, client, gasAccountID, gasCharge, "GBP", gasDesc, gasRef, ""); err != nil {
				return fmt.Errorf("deposit gas for %s day %d: %w", cust.legalName, day, err)
			}
		}

		fmt.Printf("  %s: Elec £%.2f + Gas £%.2f = £%.2f (30 days, avg %.0f kWh/day elec)\n",
			cust.legalName, totalElecGBP, totalGasGBP, totalElecGBP+totalGasGBP, cust.dailyKwhAvg)
	}

	return nil
}
