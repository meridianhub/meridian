package cmd

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
)

// Sentinel errors for CEL expression evaluation.
var (
	ErrAttributeFormat      = errors.New("attribute must be in key=value format")
	ErrValidationReturnType = errors.New("validation expression must return boolean")
	ErrBucketKeyReturnType  = errors.New("bucket key expression must return string")
	ErrErrorMsgReturnType   = errors.New("error message expression must return string")
	ErrValidationFailed     = errors.New("validation failed")
)

// SimulateResult contains the outcome of a simulation run.
type SimulateResult struct {
	// Input parameters
	TenantID   string            `json:"tenant_id"`
	Instrument string            `json:"instrument"`
	Version    int               `json:"version,omitempty"`
	Amount     string            `json:"amount"`
	Attributes map[string]string `json:"attributes,omitempty"`
	ValidFrom  *time.Time        `json:"valid_from,omitempty"`
	ValidTo    *time.Time        `json:"valid_to,omitempty"`
	Source     string            `json:"source,omitempty"`

	// Instrument definition (excluded from JSON)
	InstrumentDef *pb.InstrumentDefinition `json:"-"`

	// Validation results
	ValidationPassed bool     `json:"validation_passed"`
	ValidationErrors []string `json:"validation_errors,omitempty"`

	// Bucket key results
	BucketID       string   `json:"bucket_id"`
	BucketIDErrors []string `json:"bucket_id_errors,omitempty"`

	// Position preview
	PositionPreview *PositionPreview `json:"position_preview,omitempty"`

	// Error message (if validation failed)
	CustomErrorMessage string `json:"error_message,omitempty"`
}

// PositionPreview represents a preview of the position record structure.
type PositionPreview struct {
	InstrumentCode string            `json:"instrument_code"`
	Version        int               `json:"version"`
	BucketID       string            `json:"bucket_id"`
	Amount         string            `json:"amount"`
	Dimension      string            `json:"dimension"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	ValidFrom      *time.Time        `json:"valid_from,omitempty"`
	ValidTo        *time.Time        `json:"valid_to,omitempty"`
	Source         string            `json:"source,omitempty"`
}

var (
	tenantID   string
	instrument string
	version    int
	amount     string
	attrs      []string
	validFrom  string
	validTo    string
	source     string
	outputJSON bool
)

// simulateCmd represents the simulate command.
var simulateCmd = &cobra.Command{
	Use:   "simulate",
	Short: "Simulate a transaction for an instrument",
	Long: `Simulate runs a full transaction dry run showing validation,
bucket ID generation, and position preview.

This command fetches the instrument definition from the Reference Data Service,
evaluates CEL expressions for validation and bucket key generation, and
outputs a formatted report of the results.

Examples:
  # Basic simulation
  instrument-cli simulate --tenant=acme_bank --instrument=USD --amount=100.00

  # With attributes for non-fungible instruments
  instrument-cli simulate --tenant=acme_bank --instrument=CARBON_CREDIT \
    --amount=50.00 --attr=vintage_year=2024 --attr=registry=VERRA

  # With validity period
  instrument-cli simulate --tenant=acme_bank --instrument=VOUCHER \
    --amount=10 --valid-from=2024-01-01T00:00:00Z --valid-to=2024-12-31T23:59:59Z

  # JSON output for scripting
  instrument-cli simulate --tenant=acme_bank --instrument=USD --amount=100 --json

  # Local development (insecure connection)
  instrument-cli simulate --insecure --tenant=acme_bank --instrument=USD --amount=100`,
	RunE:          runSimulateWrapper,
	SilenceErrors: true, // We handle errors ourselves for proper exit codes
}

func init() {
	rootCmd.AddCommand(simulateCmd)

	simulateCmd.Flags().StringVar(&tenantID, "tenant", "", "Tenant ID (required)")
	simulateCmd.Flags().StringVar(&instrument, "instrument", "", "Instrument code (required)")
	simulateCmd.Flags().IntVar(&version, "version", 0, "Instrument version (0 = latest active)")
	simulateCmd.Flags().StringVar(&amount, "amount", "", "Transaction amount (required)")
	simulateCmd.Flags().StringSliceVar(&attrs, "attr", nil, "Attributes as key=value (repeatable)")
	simulateCmd.Flags().StringVar(&validFrom, "valid-from", "", "Validity start time (RFC3339)")
	simulateCmd.Flags().StringVar(&validTo, "valid-to", "", "Validity end time (RFC3339)")
	simulateCmd.Flags().StringVar(&source, "source", "", "Source identifier")
	simulateCmd.Flags().BoolVar(&outputJSON, "json", false, "Output as JSON")

	_ = simulateCmd.MarkFlagRequired("tenant")
	_ = simulateCmd.MarkFlagRequired("instrument")
	_ = simulateCmd.MarkFlagRequired("amount")
}

// runSimulateWrapper handles exit codes for the simulate command.
// This wrapper exists to avoid exitAfterDefer linter warnings by keeping
// os.Exit calls separate from functions with deferred cleanup.
func runSimulateWrapper(cmd *cobra.Command, args []string) error {
	err := runSimulate(cmd, args)
	if err == nil {
		return nil
	}

	// Handle validation failure - exit 1 (output already printed)
	if errors.Is(err, ErrValidationFailed) {
		os.Exit(1)
	}

	// For other errors, print and exit
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
	return nil // unreachable but satisfies compiler
}

func runSimulate(cmd *cobra.Command, _ []string) error {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	// Parse attributes
	attributes, err := parseAttributes(attrs)
	if err != nil {
		return fmt.Errorf("invalid attributes: %w", err)
	}

	// Parse validity times
	var validFromTime, validToTime *time.Time
	if validFrom != "" {
		t, err := time.Parse(time.RFC3339, validFrom)
		if err != nil {
			return fmt.Errorf("invalid valid-from time: %w", err)
		}
		validFromTime = &t
	}
	if validTo != "" {
		t, err := time.Parse(time.RFC3339, validTo)
		if err != nil {
			return fmt.Errorf("invalid valid-to time: %w", err)
		}
		validToTime = &t
	}

	// Fetch instrument definition
	instrDef, err := fetchInstrument(ctx, tenantID, instrument, version)
	if err != nil {
		handleGRPCError(err, fmt.Sprintf("Instrument %s", instrument))
		return fmt.Errorf("fetch instrument: %w", err)
	}

	// Run simulation
	result := simulate(instrDef, attributes, amount, validFromTime, validToTime, source)
	result.TenantID = tenantID
	result.Instrument = instrument
	result.Version = version
	result.Amount = amount
	result.Attributes = attributes
	result.ValidFrom = validFromTime
	result.ValidTo = validToTime
	result.Source = source
	result.InstrumentDef = instrDef

	// Output results
	if outputJSON {
		if err := printJSONResult(result); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		printFormattedResult(result)
	}

	// Return error if validation failed (wrapper handles exit code)
	if !result.ValidationPassed {
		return ErrValidationFailed
	}

	return nil
}

func parseAttributes(attrs []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, attr := range attrs {
		parts := strings.SplitN(attr, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: %q", ErrAttributeFormat, attr)
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}

func fetchInstrument(ctx context.Context, tenantID, code string, version int) (*pb.InstrumentDefinition, error) {
	// Configure transport credentials
	var creds credentials.TransportCredentials
	if insecureMode {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	// Create gRPC connection
	conn, err := grpc.NewClient(serviceURL, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			slog.Debug("failed to close gRPC connection", "error", closeErr)
		}
	}()

	client := pb.NewReferenceDataServiceClient(conn)

	// Add tenant header
	ctx = metadata.AppendToOutgoingContext(ctx, "x-tenant-id", tenantID)

	// Fetch instrument
	resp, err := client.RetrieveInstrument(ctx, &pb.RetrieveInstrumentRequest{
		Code:    code,
		Version: int32(version),
	})
	if err != nil {
		return nil, err
	}

	return resp.Instrument, nil
}

func simulate(
	instrDef *pb.InstrumentDefinition,
	attributes map[string]string,
	amount string,
	validFrom, validTo *time.Time,
	source string,
) *SimulateResult {
	result := &SimulateResult{
		ValidationPassed: true,
	}

	// Create CEL compiler
	compiler, err := refcel.NewCompiler()
	if err != nil {
		result.ValidationPassed = false
		result.ValidationErrors = append(result.ValidationErrors, fmt.Sprintf("failed to create CEL compiler: %v", err))
		return result
	}

	// Build CEL input
	input := buildCELInput(attributes, amount, validFrom, validTo, source)
	bucketInput := map[string]any{"attributes": attributes}

	// Run validation expression
	if instrDef.ValidationExpression != "" {
		validationPassed, validationErr := evalValidation(compiler, instrDef.ValidationExpression, input)
		if validationErr != nil {
			result.ValidationPassed = false
			result.ValidationErrors = append(result.ValidationErrors, validationErr.Error())
		} else if !validationPassed {
			result.ValidationPassed = false
			result.ValidationErrors = append(result.ValidationErrors, "validation expression returned false")
		}
	}

	// Generate bucket key
	if instrDef.FungibilityKeyExpression != "" {
		bucketKey, bucketErr := evalBucketKey(compiler, instrDef.FungibilityKeyExpression, bucketInput)
		if bucketErr != nil {
			result.BucketIDErrors = append(result.BucketIDErrors, bucketErr.Error())
		} else {
			result.BucketID = bucketKey
		}
	} else {
		// Default bucket key: instrument code only (fully fungible)
		result.BucketID = generateDefaultBucketKey(instrDef.Code, int(instrDef.Version))
	}

	// Generate custom error message if validation failed
	if !result.ValidationPassed && instrDef.ErrorMessageExpression != "" {
		errorMsg, _ := evalErrorMessage(compiler, instrDef.ErrorMessageExpression, input)
		result.CustomErrorMessage = errorMsg
	}

	// Build position preview
	result.PositionPreview = &PositionPreview{
		InstrumentCode: instrDef.Code,
		Version:        int(instrDef.Version),
		BucketID:       result.BucketID,
		Amount:         amount,
		Dimension:      dimensionToString(instrDef.Dimension),
		Attributes:     attributes,
		ValidFrom:      validFrom,
		ValidTo:        validTo,
		Source:         source,
	}

	return result
}

func buildCELInput(attributes map[string]string, amount string, validFrom, validTo *time.Time, source string) map[string]any {
	input := map[string]any{
		"attributes": attributes,
		"amount":     amount,
		"source":     source,
	}
	if validFrom != nil {
		input["valid_from"] = *validFrom
	} else {
		input["valid_from"] = time.Time{}
	}
	if validTo != nil {
		input["valid_to"] = *validTo
	} else {
		input["valid_to"] = time.Time{}
	}
	return input
}

func evalValidation(compiler *refcel.Compiler, expr string, input map[string]any) (bool, error) {
	prg, err := compiler.CompileValidation(expr)
	if err != nil {
		return false, fmt.Errorf("compilation failed: %w", err)
	}

	out, _, evalErr := prg.Eval(input)
	if evalErr != nil {
		return false, fmt.Errorf("evaluation failed: %w", evalErr)
	}

	b, ok := out.Value().(bool)
	if !ok {
		return false, ErrValidationReturnType
	}

	return b, nil
}

func evalBucketKey(compiler *refcel.Compiler, expr string, input map[string]any) (string, error) {
	prg, err := compiler.CompileBucketKey(expr)
	if err != nil {
		return "", fmt.Errorf("compilation failed: %w", err)
	}

	out, _, evalErr := prg.Eval(input)
	if evalErr != nil {
		return "", fmt.Errorf("evaluation failed: %w", evalErr)
	}

	s, ok := out.Value().(string)
	if !ok {
		return "", ErrBucketKeyReturnType
	}

	return s, nil
}

func evalErrorMessage(compiler *refcel.Compiler, expr string, input map[string]any) (string, error) {
	// Error message uses validation environment (has access to all variables)
	prg, err := compiler.CompileValidation(expr)
	if err != nil {
		return "", fmt.Errorf("compilation failed: %w", err)
	}

	out, _, evalErr := prg.Eval(input)
	if evalErr != nil {
		return "", fmt.Errorf("evaluation failed: %w", evalErr)
	}

	s, ok := out.Value().(string)
	if !ok {
		return "", ErrErrorMsgReturnType
	}

	return s, nil
}

// generateDefaultBucketKey generates a bucket key using the same algorithm as the CEL bucket_key function.
func generateDefaultBucketKey(code string, version int) string {
	hasher := sha256.New()

	// Write code length prefix and value
	lenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBytes, uint32(len(code)))
	hasher.Write(lenBytes)
	hasher.Write([]byte(code))

	// Write version as string
	versionStr := fmt.Sprintf("%d", version)
	binary.BigEndian.PutUint32(lenBytes, uint32(len(versionStr)))
	hasher.Write(lenBytes)
	hasher.Write([]byte(versionStr))

	return hex.EncodeToString(hasher.Sum(nil))
}

func dimensionToString(d pb.Dimension) string {
	switch d {
	case pb.Dimension_DIMENSION_UNSPECIFIED:
		return "UNKNOWN"
	case pb.Dimension_DIMENSION_CURRENCY:
		return "MONETARY"
	case pb.Dimension_DIMENSION_ENERGY:
		return "ENERGY"
	case pb.Dimension_DIMENSION_MASS:
		return "MASS"
	case pb.Dimension_DIMENSION_VOLUME:
		return "VOLUME"
	case pb.Dimension_DIMENSION_TIME:
		return "TIME"
	case pb.Dimension_DIMENSION_COMPUTE:
		return "COMPUTE"
	case pb.Dimension_DIMENSION_CARBON:
		return "CARBON"
	case pb.Dimension_DIMENSION_DATA:
		return "DATA"
	case pb.Dimension_DIMENSION_COUNT:
		return "COUNT"
	}
	return "UNKNOWN"
}

func printFormattedResult(result *SimulateResult) {
	fmt.Println()
	fmt.Println("╭─────────────────────────────────────────────────────────────────────────╮")
	fmt.Println("│                    INSTRUMENT SIMULATION REPORT                         │")
	fmt.Println("╰─────────────────────────────────────────────────────────────────────────╯")
	fmt.Println()

	printInputSection(result)
	printValidationSection(result)
	printBucketIDSection(result)
	printPositionPreviewSection(result)
	fmt.Println()
}

func printInputSection(result *SimulateResult) {
	fmt.Println("┌─ INPUT ────────────────────────────────────────────────────────────────┐")
	fmt.Printf("│ Tenant:     %-60s│\n", result.TenantID)
	fmt.Printf("│ Instrument: %-60s│\n", result.Instrument)
	if result.InstrumentDef != nil {
		fmt.Printf("│ Version:    %-60s│\n", fmt.Sprintf("%d (%s)", result.InstrumentDef.Version, result.InstrumentDef.DisplayName))
	}
	fmt.Printf("│ Amount:     %-60s│\n", result.Amount)
	printAttributesInSection(result.Attributes)
	if result.ValidFrom != nil {
		fmt.Printf("│ Valid From: %-60s│\n", result.ValidFrom.Format(time.RFC3339))
	}
	if result.ValidTo != nil {
		fmt.Printf("│ Valid To:   %-60s│\n", result.ValidTo.Format(time.RFC3339))
	}
	if result.Source != "" {
		fmt.Printf("│ Source:     %-60s│\n", result.Source)
	}
	fmt.Println("└────────────────────────────────────────────────────────────────────────┘")
	fmt.Println()
}

func printAttributesInSection(attributes map[string]string) {
	if len(attributes) == 0 {
		return
	}
	fmt.Println("│ Attributes:                                                            │")
	keys := sortedKeys(attributes)
	for _, k := range keys {
		fmt.Printf("│   %s = %-57s│\n", k, attributes[k])
	}
}

func printValidationSection(result *SimulateResult) {
	fmt.Println("┌─ VALIDATION ───────────────────────────────────────────────────────────┐")
	if result.ValidationPassed {
		fmt.Println("│ Status: ✓ PASSED                                                       │")
	} else {
		fmt.Println("│ Status: ✗ FAILED                                                       │")
		for _, err := range result.ValidationErrors {
			fmt.Printf("│ Error:  %-64s│\n", truncate(err, 64))
		}
		if result.CustomErrorMessage != "" {
			fmt.Printf("│ Message: %-63s│\n", truncate(result.CustomErrorMessage, 63))
		}
	}
	fmt.Println("└────────────────────────────────────────────────────────────────────────┘")
	fmt.Println()
}

func printBucketIDSection(result *SimulateResult) {
	fmt.Println("┌─ BUCKET ID ────────────────────────────────────────────────────────────┐")
	if len(result.BucketIDErrors) > 0 {
		fmt.Println("│ Status: ✗ GENERATION FAILED                                           │")
		for _, err := range result.BucketIDErrors {
			fmt.Printf("│ Error:  %-64s│\n", truncate(err, 64))
		}
	} else {
		fmt.Println("│ Status: ✓ GENERATED                                                    │")
		fmt.Printf("│ ID:     %-64s│\n", result.BucketID)
	}
	fmt.Println("└────────────────────────────────────────────────────────────────────────┘")
	fmt.Println()
}

func printPositionPreviewSection(result *SimulateResult) {
	if result.PositionPreview == nil {
		return
	}
	fmt.Println("┌─ POSITION PREVIEW ─────────────────────────────────────────────────────┐")
	fmt.Printf("│ instrument_code: %-55s│\n", result.PositionPreview.InstrumentCode)
	fmt.Printf("│ version:         %-55s│\n", fmt.Sprintf("%d", result.PositionPreview.Version))
	fmt.Printf("│ bucket_id:       %-55s│\n", truncate(result.PositionPreview.BucketID, 55))
	fmt.Printf("│ amount:          %-55s│\n", result.PositionPreview.Amount)
	fmt.Printf("│ dimension:       %-55s│\n", result.PositionPreview.Dimension)
	if len(result.PositionPreview.Attributes) > 0 {
		fmt.Println("│ attributes:                                                            │")
		keys := sortedKeys(result.PositionPreview.Attributes)
		for _, k := range keys {
			fmt.Printf("│   %s: %-60s│\n", k, result.PositionPreview.Attributes[k])
		}
	}
	fmt.Println("└────────────────────────────────────────────────────────────────────────┘")
}

func printJSONResult(result *SimulateResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncate(s string, maxLen int) string {
	if maxLen < 4 {
		maxLen = 4 // Minimum length to show "..."
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
