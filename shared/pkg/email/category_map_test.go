package email_test

import (
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTemplateCategoryMap_AllEntriesHaveValidCategories(t *testing.T) {
	validCategories := map[string]bool{
		email.CategoryTransactional: true,
		email.CategoryOperational:   true,
		email.CategoryMarketing:     true,
	}

	for template, category := range email.DefaultTemplateCategoryMap {
		assert.True(t, validCategories[category],
			"template %q has invalid category %q", template, category)
	}
}

func TestDefaultTemplateCategoryMap_TransactionalTemplates(t *testing.T) {
	transactional := []string{
		"dunning-notice",
		"account-frozen",
		"payment-confirmation",
		"invoice-delivery",
	}
	for _, tmpl := range transactional {
		assert.Equal(t, email.CategoryTransactional, email.DefaultTemplateCategoryMap[tmpl],
			"template %q should be TRANSACTIONAL", tmpl)
	}
}

func TestDefaultTemplateCategoryMap_OperationalTemplates(t *testing.T) {
	operational := []string{
		"dunning-resolved",
		"welcome",
		"service-update",
	}
	for _, tmpl := range operational {
		assert.Equal(t, email.CategoryOperational, email.DefaultTemplateCategoryMap[tmpl],
			"template %q should be OPERATIONAL", tmpl)
	}
}

func TestDefaultTemplateCategoryMap_MarketingTemplates(t *testing.T) {
	marketing := []string{
		"promotional-offer",
	}
	for _, tmpl := range marketing {
		assert.Equal(t, email.CategoryMarketing, email.DefaultTemplateCategoryMap[tmpl],
			"template %q should be MARKETING", tmpl)
	}
}

func TestCategoryEnforcement_MismatchRejectedForAllMapEntries(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		email.DefaultTemplateCategoryMap,
		slog.Default(),
	)

	// For each template in the map, try declaring a wrong category and verify rejection.
	for tmpl, correctCategory := range email.DefaultTemplateCategoryMap {
		wrongCategory := email.CategoryMarketing
		if correctCategory == email.CategoryMarketing {
			wrongCategory = email.CategoryTransactional
		}

		allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", tmpl, wrongCategory)
		require.ErrorIs(t, err, email.ErrCategoryMismatch, "template %q should reject mismatched category", tmpl)
		assert.False(t, allowed, "template %q should not be allowed with wrong category", tmpl)
		assert.Contains(t, reason, "category mismatch", "template %q reason should mention mismatch", tmpl)
	}
}

func TestCategoryEnforcement_CorrectCategoryAllowed(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		email.DefaultTemplateCategoryMap,
		slog.Default(),
	)

	// Transactional templates should always be allowed.
	for _, tmpl := range []string{"dunning-notice", "account-frozen", "payment-confirmation", "invoice-delivery"} {
		allowed, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", tmpl, email.CategoryTransactional)
		require.NoError(t, err, "template %q with correct category should not error", tmpl)
		assert.True(t, allowed, "template %q with correct TRANSACTIONAL category should be allowed", tmpl)
	}

	// Operational templates should be allowed by default (no explicit opt-out).
	for _, tmpl := range []string{"dunning-resolved", "welcome", "service-update"} {
		allowed, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", tmpl, email.CategoryOperational)
		require.NoError(t, err, "template %q with correct category should not error", tmpl)
		assert.True(t, allowed, "template %q with correct OPERATIONAL category should be allowed", tmpl)
	}
}

func TestCategoryEnforcement_MarketingCorrectCategorySuppressedWithoutOptIn(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{}, // no explicit opt-in
		email.DefaultTemplateCategoryMap,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "promotional-offer", email.CategoryMarketing)
	require.NoError(t, err)
	assert.False(t, allowed, "marketing should be suppressed without opt-in")
	assert.Contains(t, reason, "no explicit opt-in")
}

func TestCategoryEnforcement_UnknownTemplatePassesThrough(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		email.DefaultTemplateCategoryMap,
		slog.Default(),
	)

	// A template not in the map should skip the map check.
	allowed, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "custom-template", email.CategoryOperational)
	require.NoError(t, err)
	assert.True(t, allowed)
}
