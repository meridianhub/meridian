package email

import (
	"context"
	"fmt"
	"log/slog"
)

// Category constants matching the database CHECK constraint values and proto enum names.
const (
	CategoryTransactional = "TRANSACTIONAL"
	CategoryOperational   = "OPERATIONAL"
	CategoryMarketing     = "MARKETING"
)

// Sentinel errors for preference enforcement.
var (
	ErrCategoryMismatch = fmt.Errorf("email: template category mismatch")
	ErrMissingCategory  = fmt.Errorf("email: missing or unknown category")
)

// PreferenceEnforcer checks communication preferences before sending.
type PreferenceEnforcer struct {
	prefRepo            PreferenceRepository
	templateCategoryMap map[string]string // template name -> category
	logger              *slog.Logger
}

// NewPreferenceEnforcer creates a new enforcer.
// templateCategoryMap maps template names to their expected category
// (e.g., "dunning-notice" -> "TRANSACTIONAL").
func NewPreferenceEnforcer(prefRepo PreferenceRepository, templateCategoryMap map[string]string, logger *slog.Logger) *PreferenceEnforcer {
	if prefRepo == nil {
		panic("email: PreferenceRepository must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PreferenceEnforcer{
		prefRepo:            prefRepo,
		templateCategoryMap: templateCategoryMap,
		logger:              logger,
	}
}

// ShouldSend checks whether a message should be sent based on the party's
// communication preferences. Returns (allowed, reason, error) where reason
// explains why the message was suppressed (empty string if allowed).
//
// 6-step enforcement algorithm:
//  1. Validate declaredCategory against templateCategoryMap (reject mismatches)
//  2. TRANSACTIONAL always allowed (bypass preference checks)
//  3. Check global unsubscribe (GDPR Art. 21)
//  4. Check channel+category preference
//  5. Default: OPERATIONAL=allow, MARKETING=suppress (GDPR Art. 7)
//  6. Suppression check (handled separately in processor - not in this method)
func (e *PreferenceEnforcer) ShouldSend(ctx context.Context, tenantID, partyID, channel, templateName, declaredCategory string) (bool, string, error) {
	// Step 1: Validate category against template map (if configured).
	if e.templateCategoryMap != nil {
		if expected, ok := e.templateCategoryMap[templateName]; ok {
			if expected != declaredCategory {
				return false, fmt.Sprintf("category mismatch: template %q expects %s, got %s",
					templateName, expected, declaredCategory), ErrCategoryMismatch
			}
		}
	}

	// Validate that the declared category is known.
	if !isValidCategory(declaredCategory) {
		return false, fmt.Sprintf("unknown category: %s", declaredCategory), ErrMissingCategory
	}

	// Step 2: TRANSACTIONAL always allowed - bypass all preference checks.
	if declaredCategory == CategoryTransactional {
		e.logger.Debug("preference check: transactional message allowed",
			"party_id", partyID, "template", templateName)
		return true, "", nil
	}

	// Step 3: Check global unsubscribe.
	globalUnsub, err := e.prefRepo.GetGlobalUnsubscribe(ctx, tenantID, partyID)
	if err != nil {
		return false, "", fmt.Errorf("email: preference check failed: %w", err)
	}
	if globalUnsub {
		e.logger.Info("preference check: globally unsubscribed",
			"party_id", partyID, "category", declaredCategory)
		return false, "party has globally unsubscribed", nil
	}

	// Step 4: Check channel+category preference.
	pref, err := e.prefRepo.GetPreference(ctx, tenantID, partyID, channel, declaredCategory)
	if err != nil {
		return false, "", fmt.Errorf("email: preference check failed: %w", err)
	}
	if pref != nil {
		if !pref.OptedIn {
			e.logger.Info("preference check: opted out",
				"party_id", partyID, "channel", channel, "category", declaredCategory)
			return false, fmt.Sprintf("party opted out of %s/%s", channel, declaredCategory), nil
		}
		return true, "", nil
	}

	// Step 5: No explicit preference set - apply defaults.
	switch declaredCategory {
	case CategoryOperational:
		// Operational messages allowed by default (opt-out model).
		return true, "", nil
	case CategoryMarketing:
		// Marketing messages suppressed by default (opt-in model, GDPR Art. 7).
		e.logger.Info("preference check: marketing suppressed (no opt-in)",
			"party_id", partyID, "channel", channel)
		return false, "no explicit opt-in for marketing", nil
	default:
		return false, fmt.Sprintf("unknown category: %s", declaredCategory), ErrMissingCategory
	}
}

func isValidCategory(category string) bool {
	switch category {
	case CategoryTransactional, CategoryOperational, CategoryMarketing:
		return true
	default:
		return false
	}
}
