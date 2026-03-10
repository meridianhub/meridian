package generator_test

// Golden file tests verify that the economy generator produces structurally
// correct manifests for known industry descriptions. Tests use cached LLM
// responses stored in testdata/golden/ to keep CI fast and deterministic.
// When ANTHROPIC_API_KEY is set and a cache entry is missing or stale (>7 days),
// the real LLM is called and the response is saved for future runs.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
)

const goldenCacheTTL = 7 * 24 * time.Hour

// goldenCacheEntry is the on-disk format for a cached LLM response.
type goldenCacheEntry struct {
	CachedAt     time.Time `json:"cached_at"`
	ManifestYAML string    `json:"manifest_yaml"`
}

// goldenTestCase defines structural expectations for a generated manifest.
type goldenTestCase struct {
	// Name is used as the cache file name and subtest name.
	Name string
	// Description is the natural-language input to the generator.
	Description string
	// Industry hint passed as a generation preference.
	Industry string
	// ExpectInstrumentCodes lists instrument codes that must appear in the manifest.
	ExpectInstrumentCodes []string
	// ExpectAccountTypeCodes lists account type codes that must appear.
	ExpectAccountTypeCodes []string
	// ExpectAtLeastOneSaga asserts that at least one saga is defined.
	ExpectAtLeastOneSaga bool
}

var goldenTestCases = []goldenTestCase{
	{
		Name:                  "ev_charging_uk",
		Description:           "EV charging network in the UK with fleet customers, time-of-use pricing, and OCPP charger integration",
		Industry:              "energy",
		ExpectInstrumentCodes: []string{"GBP", "KWH"},
		ExpectAtLeastOneSaga:  true,
	},
	{
		Name:                  "saas_billing_usd",
		Description:           "SaaS platform with monthly subscription billing, usage-based pricing, and multi-currency support",
		Industry:              "saas",
		ExpectInstrumentCodes: []string{"USD"},
		ExpectAtLeastOneSaga:  true,
	},
	{
		Name:                 "carbon_offset_marketplace",
		Description:          "Carbon offset marketplace that issues, transfers and retires verified carbon credits for compliance buyers",
		Industry:             "carbon",
		ExpectAtLeastOneSaga: true,
	},
	{
		Name:                  "neobank_multi_currency",
		Description:           "Neobank offering current accounts in GBP and EUR with instant transfers and card spending",
		Industry:              "banking",
		ExpectInstrumentCodes: []string{"GBP", "EUR"},
		ExpectAtLeastOneSaga:  true,
	},
	{
		Name:                  "gpu_compute_billing",
		Description:           "AI infrastructure provider that bills customers per GPU-hour with prepaid credit top-ups and overage limits",
		Industry:              "compute",
		ExpectInstrumentCodes: []string{"USD"},
		ExpectAtLeastOneSaga:  true,
	},
}

// TestGoldenGeneration verifies structural properties of generated manifests
// for a set of known industry descriptions.
func TestGoldenGeneration(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	cacheDir := filepath.Join("testdata", "golden")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}

	for _, tc := range goldenTestCases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			manifestYAML := loadOrGenerateGolden(t, tc, cacheDir, apiKey)
			assertManifestStructure(t, tc, manifestYAML)
		})
	}
}

// loadOrGenerateGolden returns the manifest YAML for a test case, either from
// the cache or by calling the real LLM (when the API key is available).
func loadOrGenerateGolden(t *testing.T, tc goldenTestCase, cacheDir, apiKey string) string {
	t.Helper()

	cacheFile := filepath.Join(cacheDir, tc.Name+".json")
	entry, cacheErr := readCacheEntry(cacheFile)

	cacheHit := cacheErr == nil
	cacheFresh := cacheHit && time.Since(entry.CachedAt) < goldenCacheTTL

	if cacheFresh {
		return entry.ManifestYAML
	}

	if apiKey == "" {
		if cacheHit {
			// Stale cache is still usable for structural checks when no key is available.
			t.Logf("golden cache for %q is stale (%s old); re-using because ANTHROPIC_API_KEY is not set",
				tc.Name, time.Since(entry.CachedAt).Round(time.Hour))
			return entry.ManifestYAML
		}
		t.Skipf("no cached response for %q and ANTHROPIC_API_KEY is not set; skipping", tc.Name)
	}

	// Call the real LLM and persist the result.
	manifestYAML := generateWithRealLLM(t, tc, apiKey)
	if err := writeCacheEntry(cacheFile, goldenCacheEntry{
		CachedAt:     time.Now(),
		ManifestYAML: manifestYAML,
	}); err != nil {
		// Non-fatal: log and continue; the test still runs with the live result.
		t.Logf("warning: failed to write golden cache for %q: %v", tc.Name, err)
	}
	return manifestYAML
}

// generateWithRealLLM calls GenerateManifest using the real Anthropic API.
func generateWithRealLLM(t *testing.T, tc goldenTestCase, apiKey string) string {
	t.Helper()

	llmClient := generator.NewClaudeLLMClient(apiKey, "")

	// Dry-run validator always returns valid — structural assertions are checked
	// after generation rather than through the validate-fix loop.
	alwaysValid := generator.NewManifestValidatorAdapter(
		func(_ context.Context, _ string) (*generator.ValidationResult, error) {
			return &generator.ValidationResult{Valid: true}, nil
		},
	)

	svc, err := generator.NewGeneratorService(
		buildMinimalRegistry(),
		nil,
		emptyFS(),
		nil,
		generator.WithLLMClient(llmClient),
		generator.WithValidator(alwaysValid),
	)
	require.NoError(t, err, "create generator service for %q", tc.Name)

	prefs := &controlplanev1.GenerationPreferences{
		Industry: tc.Industry,
	}
	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: tc.Description,
		Preferences: prefs,
	})
	require.NoError(t, err, "GenerateManifest for %q", tc.Name)
	require.NotEmpty(t, resp.GetManifestYaml(), "LLM returned empty manifest for %q", tc.Name)

	return resp.GetManifestYaml()
}

// assertManifestStructure validates structural properties of a generated manifest YAML.
func assertManifestStructure(t *testing.T, tc goldenTestCase, manifestYAML string) {
	t.Helper()

	require.NotEmpty(t, manifestYAML, "manifest YAML must not be empty for %q", tc.Name)

	// Must parse as valid YAML.
	var doc map[string]interface{}
	err := yaml.Unmarshal([]byte(manifestYAML), &doc)
	require.NoError(t, err, "manifest for %q must be valid YAML", tc.Name)
	require.NotNil(t, doc, "manifest YAML must not be empty document for %q", tc.Name)

	instrumentCodes := extractStringField(doc, "instruments", "code")
	accountTypeCodes := extractStringField(doc, "account_types", "code")
	sagaNames := extractStringField(doc, "sagas", "name")

	for _, code := range tc.ExpectInstrumentCodes {
		assert.Truef(t, containsIgnoreCase(instrumentCodes, code),
			"expected instrument code %q in manifest for %q; got: %v", code, tc.Name, instrumentCodes)
	}

	for _, code := range tc.ExpectAccountTypeCodes {
		assert.Truef(t, containsIgnoreCase(accountTypeCodes, code),
			"expected account type code %q in manifest for %q; got: %v", code, tc.Name, accountTypeCodes)
	}

	if tc.ExpectAtLeastOneSaga {
		assert.NotEmptyf(t, sagaNames, "expected at least one saga in manifest for %q", tc.Name)
	}
}

// extractStringField returns the named string field from each item in a top-level YAML list.
func extractStringField(doc map[string]interface{}, listKey, fieldKey string) []string {
	raw, ok := doc[listKey]
	if !ok {
		return nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := m[fieldKey].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// containsIgnoreCase reports whether target appears in list (case-insensitive).
func containsIgnoreCase(list []string, target string) bool {
	upper := strings.ToUpper(target)
	for _, v := range list {
		if strings.ToUpper(v) == upper {
			return true
		}
	}
	return false
}

// readCacheEntry reads and parses a golden cache file from disk.
func readCacheEntry(path string) (goldenCacheEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return goldenCacheEntry{}, err
	}
	var entry goldenCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return goldenCacheEntry{}, err
	}
	return entry, nil
}

// writeCacheEntry serializes a cache entry to the given path.
func writeCacheEntry(path string, entry goldenCacheEntry) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
