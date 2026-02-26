package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"github.com/meridianhub/meridian/cmd/position-tool/internal/infra"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/rebucket"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/validation"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Rebucket command flags.
var (
	rebucketInstrument string
	rebucketFrom       string
	rebucketTo         string
)

// rebucketCmd represents the rebucket command.
var rebucketCmd = &cobra.Command{
	Use:   "rebucket",
	Short: "Recalculate bucket keys for existing positions",
	Long: `Recalculate bucket keys for existing positions after instrument changes.

The rebucket command re-evaluates the bucket key expression for positions,
typically needed after an instrument definition change that affects bucket
key generation.

Use Cases:
  - Instrument fungibility expression changed
  - Attribute-based bucketing rules updated
  - Migration from default to custom bucket keys

Safety Features:
  - Dry-run mode shows changes without applying
  - Date range filtering to limit scope
  - Audit trail of all changes
  - Atomic batch updates

WARNING: This operation modifies existing position records. Always use
--dry-run first to preview changes.

Exit Codes:
  0 - Success
  1 - Failure (error occurred)

Examples:
  # Preview rebucketing for all CARBON_CREDIT positions
  position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT --dry-run

  # Rebucket positions created in a specific date range
  position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT \
    --from=2024-01-01 --to=2024-06-30

  # Execute rebucketing
  position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT`,
	RunE:          runRebucketWrapper,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(rebucketCmd)

	rebucketCmd.Flags().StringVar(&rebucketInstrument, "instrument", "",
		"Instrument code to rebucket (required)")
	rebucketCmd.Flags().StringVar(&rebucketFrom, "from", "",
		"Only rebucket positions created from this date (YYYY-MM-DD or RFC3339)")
	rebucketCmd.Flags().StringVar(&rebucketTo, "to", "",
		"Only rebucket positions created to this date (YYYY-MM-DD or RFC3339)")

	_ = rebucketCmd.MarkFlagRequired("instrument")
}

// runRebucketWrapper handles exit codes for the rebucket command.
func runRebucketWrapper(cmd *cobra.Command, args []string) error {
	err := runRebucket(cmd, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return nil
}

func runRebucket(_ *cobra.Command, _ []string) error {
	// Validate required flags
	if err := validateCommonFlags(); err != nil {
		return err
	}

	// Parse date filters if provided
	var fromTime, toTime *time.Time
	if rebucketFrom != "" {
		t, err := parseDate(rebucketFrom)
		if err != nil {
			return fmt.Errorf("invalid --from date: %w", err)
		}
		fromTime = &t
	}
	if rebucketTo != "" {
		t, err := parseDate(rebucketTo)
		if err != nil {
			return fmt.Errorf("invalid --to date: %w", err)
		}
		toTime = &t
	}

	// Validate date range
	if fromTime != nil && toTime != nil && fromTime.After(*toTime) {
		return ErrDateRangeInvalid
	}

	// Set up graceful shutdown context
	ctx, cancel := ShutdownContext()
	defer cancel()

	// Log configuration
	logger := slog.Default()
	logger.Info("starting rebucket",
		"tenant", tenantID,
		"instrument", rebucketInstrument,
		"from", rebucketFrom,
		"to", rebucketTo,
		"dry_run", dryRun,
	)

	start := time.Now()

	// Execute rebucket
	result, err := executeRebucket(ctx, &rebucketConfig{
		TenantID:   tenantID,
		Instrument: rebucketInstrument,
		From:       fromTime,
		To:         toTime,
		DryRun:     dryRun,
		DBUrl:      dbURL,
	})
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	// Print results
	printRebucketResult(result, elapsed)

	return nil
}

// rebucketConfig holds the configuration for a rebucket operation.
type rebucketConfig struct {
	TenantID   string
	Instrument string
	From       *time.Time
	To         *time.Time
	DryRun     bool
	DBUrl      string
}

// rebucketResult holds the results of a rebucket operation.
type rebucketResult struct {
	TotalPositions   int64
	ChangedPositions int64
	UnchangedCount   int64
	ErrorCount       int64
	Interrupted      bool
	BucketsAffected  int
	AuditEntries     int64
}

// Rebucket command errors.
var (
	// ErrNoPositionsToRebucket indicates no positions matched the filter criteria.
	ErrNoPositionsToRebucket = errors.New("no positions found matching filter criteria")
	// ErrRebucketFailed indicates the rebucketing operation failed.
	ErrRebucketFailed = errors.New("rebucketing operation failed")
	// ErrNoFungibilityExpression indicates the instrument has no fungibility key expression.
	ErrNoFungibilityExpression = errors.New("instrument has no fungibility key expression")
)

// executeRebucket performs the actual rebucket operation.
// It fetches positions by instrument, recalculates bucket keys using
// instrument CEL expressions, batch updates positions with audit logging,
// and reports progress.
func executeRebucket(ctx context.Context, cfg *rebucketConfig) (*rebucketResult, error) {
	logger := slog.Default()

	// Set up database connection
	pool, err := pgxpool.New(ctx, cfg.DBUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer pool.Close()

	// Set up tenant context
	tenantID, err := tenant.NewTenantID(cfg.TenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant ID: %w", err)
	}
	ctx = tenant.WithTenant(ctx, tenantID)

	// Initialize dependencies
	deps, err := initRebucketDependencies(ctx, pool, logger)
	if err != nil {
		return nil, err
	}
	defer deps.close()

	// Fetch positions for the instrument
	positions, err := fetchPositionsForInstrument(ctx, pool, cfg, logger)
	if err != nil {
		return nil, err
	}

	if len(positions) == 0 {
		logger.Info("no positions found for instrument",
			"instrument", cfg.Instrument,
			"tenant", cfg.TenantID,
		)
		return &rebucketResult{
			TotalPositions: 0,
		}, nil
	}

	logger.Info("found positions to rebucket",
		"count", len(positions),
		"instrument", cfg.Instrument,
	)

	// Get the instrument's fungibility key expression
	instrumentResult, err := deps.instrumentChecker.Check(ctx, cfg.Instrument, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve instrument: %w", err)
	}
	if !instrumentResult.Exists {
		return nil, fmt.Errorf("%w: %s", ErrInstrumentNotFound, cfg.Instrument)
	}

	fungibilityExpr := instrumentResult.Definition.FungibilityKeyExpression
	if fungibilityExpr == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoFungibilityExpression, cfg.Instrument)
	}

	// Build the rebucketing plan by evaluating new bucket keys
	plan, err := buildRebucketingPlan(ctx, deps.celEval, positions, fungibilityExpr, instrumentResult.Definition.Version, logger)
	if err != nil {
		return nil, err
	}

	logger.Info("rebucketing plan built",
		"total_positions", len(plan.AffectedPositions),
		"changed", plan.ChangedCount,
		"unchanged", plan.UnchangedCount,
		"errors", plan.ErrorCount,
		"bucket_mappings", len(plan.BucketMappings),
	)

	// Dry-run mode: return plan without executing
	if cfg.DryRun {
		return &rebucketResult{
			TotalPositions:   int64(len(positions)),
			ChangedPositions: plan.ChangedCount,
			UnchangedCount:   plan.UnchangedCount,
			ErrorCount:       plan.ErrorCount,
			BucketsAffected:  len(plan.BucketMappings),
		}, nil
	}

	// Check for cancellation before executing
	select {
	case <-ctx.Done():
		logger.Info("rebucket interrupted before execution")
		return &rebucketResult{
			TotalPositions: int64(len(positions)),
			Interrupted:    true,
		}, nil
	default:
	}

	// Execute the rebucketing using the executor
	result, err := executeRebucketingPlan(ctx, pool, plan, cfg.TenantID, logger)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return &rebucketResult{
				TotalPositions:   int64(len(positions)),
				ChangedPositions: result.ChangedPositions,
				Interrupted:      true,
			}, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrRebucketFailed, err)
	}

	return result, nil
}

// rebucketDependencies holds initialized dependencies for rebucketing.
type rebucketDependencies struct {
	instrumentChecker *validation.InstrumentChecker
	celEval           *infra.CELEvaluator
}

// close releases all resources held by rebucket dependencies.
func (d *rebucketDependencies) close() {
	if d.instrumentChecker != nil {
		_ = d.instrumentChecker.Close()
	}
}

// initRebucketDependencies creates all dependencies needed for rebucketing.
func initRebucketDependencies(ctx context.Context, _ *pgxpool.Pool, logger *slog.Logger) (*rebucketDependencies, error) {
	deps := &rebucketDependencies{}

	var err error
	deps.instrumentChecker, err = validation.NewInstrumentChecker(ctx, validation.InstrumentCheckerConfig{
		Target: getEnvOrDefault("REFERENCE_DATA_URL", "localhost:50051"),
		Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create instrument checker: %w", err)
	}

	deps.celEval, err = infra.NewCELEvaluatorDefault()
	if err != nil {
		deps.close()
		return nil, fmt.Errorf("failed to create CEL evaluator: %w", err)
	}

	return deps, nil
}

// positionRecord represents a position fetched from the database for rebucketing.
type positionRecord struct {
	ID             string
	AccountID      string
	InstrumentCode string
	BucketKey      string
	Amount         string
	Dimension      string
	Attributes     map[string]string
	ReferenceID    string
	CreatedAt      time.Time
	CreatedBy      string
}

// fetchPositionsForInstrument retrieves all positions for the given instrument.
func fetchPositionsForInstrument(ctx context.Context, pool *pgxpool.Pool, cfg *rebucketConfig, logger *slog.Logger) ([]positionRecord, error) {
	query := `
		SELECT id, account_id, instrument_code, bucket_key, amount, dimension,
		       attributes, reference_id, created_at, created_by
		FROM position
		WHERE instrument_code = $1
		  AND deleted_at IS NULL`

	args := []any{cfg.Instrument}
	argNum := 2

	if cfg.From != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argNum)
		args = append(args, *cfg.From)
		argNum++
	}
	if cfg.To != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argNum)
		args = append(args, *cfg.To)
	}

	query += " ORDER BY created_at ASC"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query positions: %w", err)
	}
	defer rows.Close()

	var positions []positionRecord
	for rows.Next() {
		var pos positionRecord
		var attrsJSON []byte
		var refID *string

		err := rows.Scan(
			&pos.ID,
			&pos.AccountID,
			&pos.InstrumentCode,
			&pos.BucketKey,
			&pos.Amount,
			&pos.Dimension,
			&attrsJSON,
			&refID,
			&pos.CreatedAt,
			&pos.CreatedBy,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan position: %w", err)
		}

		// Parse attributes JSON
		if len(attrsJSON) > 0 {
			if parseErr := parseAttributesJSON(attrsJSON, &pos.Attributes); parseErr != nil {
				logger.Warn("failed to parse position attributes, using empty map",
					"position_id", pos.ID,
					"error", parseErr,
				)
				pos.Attributes = make(map[string]string)
			}
		} else {
			pos.Attributes = make(map[string]string)
		}

		if refID != nil {
			pos.ReferenceID = *refID
		}

		positions = append(positions, pos)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating positions: %w", err)
	}

	return positions, nil
}

// parseAttributesJSON parses a JSON byte slice into a string map.
func parseAttributesJSON(data []byte, attrs *map[string]string) error {
	*attrs = make(map[string]string)
	// Try parsing as map[string]string first
	if err := json.Unmarshal(data, attrs); err == nil {
		return nil
	}
	// Fallback: try parsing as map[string]any and convert
	var anyMap map[string]any
	if err := json.Unmarshal(data, &anyMap); err != nil {
		return err
	}
	for k, v := range anyMap {
		(*attrs)[k] = fmt.Sprintf("%v", v)
	}
	return nil
}

// rebucketingPlan holds the plan for rebucketing positions.
type rebucketingPlan struct {
	AffectedPositions []rebucket.AffectedPosition
	BucketMappings    map[string]string
	ChangedCount      int64
	UnchangedCount    int64
	ErrorCount        int64
	InstrumentVersion int32
}

// buildRebucketingPlan evaluates new bucket keys for all positions.
func buildRebucketingPlan(
	ctx context.Context,
	celEval *infra.CELEvaluator,
	positions []positionRecord,
	fungibilityExpr string,
	instrumentVersion int32,
	logger *slog.Logger,
) (*rebucketingPlan, error) {
	plan := &rebucketingPlan{
		AffectedPositions: make([]rebucket.AffectedPosition, 0, len(positions)),
		BucketMappings:    make(map[string]string),
		InstrumentVersion: instrumentVersion,
	}

	for _, pos := range positions {
		select {
		case <-ctx.Done():
			return plan, ctx.Err()
		default:
		}

		// Evaluate the new bucket key
		newBucketKey, err := celEval.EvaluateBucketKey(fungibilityExpr, pos.Attributes)
		if err != nil {
			logger.Warn("failed to evaluate bucket key",
				"position_id", pos.ID,
				"error", err,
			)
			plan.ErrorCount++
			continue
		}

		// Check if bucket key changed
		if newBucketKey == pos.BucketKey {
			plan.UnchangedCount++
			continue
		}

		// Track the bucket mapping
		plan.BucketMappings[pos.BucketKey] = newBucketKey

		// Parse amount
		amount, err := parseDecimal(pos.Amount)
		if err != nil {
			logger.Warn("failed to parse position amount",
				"position_id", pos.ID,
				"amount", pos.Amount,
				"error", err,
			)
			plan.ErrorCount++
			continue
		}

		// Parse UUIDs
		positionID, err := parseUUID(pos.ID)
		if err != nil {
			plan.ErrorCount++
			continue
		}

		var referenceID uuid.UUID
		if pos.ReferenceID != "" {
			var parseErr error
			referenceID, parseErr = parseUUID(pos.ReferenceID)
			if parseErr != nil {
				logger.Warn("failed to parse reference ID, using nil UUID",
					"position_id", pos.ID,
					"reference_id", pos.ReferenceID,
					"error", parseErr,
				)
				// Continue with nil UUID rather than failing the entire position
			}
		}

		plan.AffectedPositions = append(plan.AffectedPositions, rebucket.AffectedPosition{
			PositionID:     positionID,
			AccountID:      pos.AccountID,
			InstrumentCode: pos.InstrumentCode,
			OldBucketKey:   pos.BucketKey,
			NewBucketKey:   newBucketKey,
			Amount:         amount,
			Dimension:      pos.Dimension,
			Attributes:     pos.Attributes,
			ReferenceID:    referenceID,
			CreatedAt:      pos.CreatedAt,
			CreatedBy:      pos.CreatedBy,
		})
		plan.ChangedCount++
	}

	return plan, nil
}

// parseDecimal parses a decimal string into shopspring/decimal.
func parseDecimal(s string) (decimal.Decimal, error) {
	return decimal.NewFromString(s)
}

// parseUUID parses a UUID string.
func parseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

// executeRebucketingPlan applies the rebucketing plan using the executor.
func executeRebucketingPlan(
	ctx context.Context,
	pool *pgxpool.Pool,
	plan *rebucketingPlan,
	adminUserID string,
	logger *slog.Logger,
) (*rebucketResult, error) {
	if len(plan.AffectedPositions) == 0 {
		return &rebucketResult{
			TotalPositions: plan.ChangedCount + plan.UnchangedCount + plan.ErrorCount,
			UnchangedCount: plan.UnchangedCount,
			ErrorCount:     plan.ErrorCount,
		}, nil
	}

	// Create the executor
	execConfig := rebucket.DefaultConfig()
	posExecutor, err := rebucket.NewExecutor(pool, execConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create position executor: %w", err)
	}

	// Build the executor plan
	execPlan := &rebucket.RebucketingPlan{
		InstrumentCode:       plan.AffectedPositions[0].InstrumentCode,
		OldInstrumentVersion: fmt.Sprintf("v%d", plan.InstrumentVersion-1),
		NewInstrumentVersion: fmt.Sprintf("v%d", plan.InstrumentVersion),
		BucketMappings:       plan.BucketMappings,
		AffectedPositions:    plan.AffectedPositions,
	}

	// Execute
	execResult, err := posExecutor.Execute(ctx, execPlan, adminUserID)
	if err != nil {
		return &rebucketResult{
			TotalPositions:   plan.ChangedCount + plan.UnchangedCount + plan.ErrorCount,
			ChangedPositions: 0,
			UnchangedCount:   plan.UnchangedCount,
			ErrorCount:       plan.ErrorCount + 1,
		}, err
	}

	return &rebucketResult{
		TotalPositions:   plan.ChangedCount + plan.UnchangedCount + plan.ErrorCount,
		ChangedPositions: execResult.PositionsUpdated,
		UnchangedCount:   plan.UnchangedCount,
		ErrorCount:       plan.ErrorCount,
		BucketsAffected:  execResult.BucketsAffected,
		AuditEntries:     execResult.AuditLogEntries,
	}, nil
}

// printRebucketResult outputs the rebucket results in a formatted report.
func printRebucketResult(result *rebucketResult, elapsed time.Duration) {
	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println("|                        REBUCKET SUMMARY                                   |")
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println()

	if dryRun {
		fmt.Println("  Mode:              DRY-RUN (no changes persisted)")
	} else {
		fmt.Println("  Mode:              LIVE")
	}

	fmt.Printf("  Instrument:        %s\n", rebucketInstrument)
	fmt.Printf("  Duration:          %s\n", formatDuration(elapsed))
	fmt.Println()

	fmt.Println("  Results:")
	fmt.Printf("    Total Positions: %d\n", result.TotalPositions)
	fmt.Printf("    Changed:         %d\n", result.ChangedPositions)
	fmt.Printf("    Unchanged:       %d\n", result.UnchangedCount)
	fmt.Printf("    Errors:          %d\n", result.ErrorCount)
	if result.BucketsAffected > 0 {
		fmt.Printf("    Buckets Affected: %d\n", result.BucketsAffected)
	}
	if result.AuditEntries > 0 {
		fmt.Printf("    Audit Entries:   %d\n", result.AuditEntries)
	}
	fmt.Println()

	if result.Interrupted {
		fmt.Println("  STATUS: INTERRUPTED")
	} else if result.ErrorCount > 0 {
		fmt.Println("  STATUS: COMPLETED WITH ERRORS")
	} else if dryRun {
		fmt.Println("  STATUS: DRY-RUN COMPLETE")
		if result.ChangedPositions > 0 {
			fmt.Printf("  Would update %d positions across %d bucket mappings\n",
				result.ChangedPositions, result.BucketsAffected)
		}
	} else {
		fmt.Println("  STATUS: SUCCESS")
	}

	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
}
