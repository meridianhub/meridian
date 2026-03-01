package topics_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"gopkg.in/yaml.v3"
)

// standardTopicPattern matches the canonical <service>.<event>.<version> naming convention.
// e.g. "position-keeping.transaction-captured.v1", "audit.events.v1"
var standardTopicPattern = regexp.MustCompile(`^[a-z][a-z0-9-]+\.[a-z][a-z0-9-]+\.v\d+$`)

// legacyTopics lists topics that are exempt from the standard naming pattern check.
// These are retained for backwards compatibility.
var legacyTopics = map[string]bool{
	topics.FinancialAccountingBookingLogControlled: true,
	topics.AuditEventsDLQV1:                        true,
}

func TestAll_NonEmpty(t *testing.T) {
	all := topics.All()
	if len(all) == 0 {
		t.Fatal("topics.All() returned an empty slice")
	}
}

func TestAllTopics_NonEmptyStrings(t *testing.T) {
	for _, topic := range topics.All() {
		if topic == "" {
			t.Error("topics.All() contains an empty string")
		}
	}
}

func TestAllTopics_NamingConvention(t *testing.T) {
	for _, topic := range topics.All() {
		if legacyTopics[topic] {
			continue
		}
		if !standardTopicPattern.MatchString(topic) {
			t.Errorf("topic %q does not follow <service>.<event>.v<N> naming convention", topic)
		}
	}
}

func TestAllTopics_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(topics.All()))
	for _, topic := range topics.All() {
		if seen[topic] {
			t.Errorf("duplicate topic name: %q", topic)
		}
		seen[topic] = true
	}
}

// topicsYAML mirrors the structure of topics.yaml for unmarshalling.
type topicsYAML struct {
	Services map[string]serviceEntry `yaml:"services"`
}

type serviceEntry struct {
	Description string       `yaml:"description"`
	Topics      []topicEntry `yaml:"topics"`
}

type topicEntry struct {
	Name       string `yaml:"name"`
	Constant   string `yaml:"constant"`
	Deprecated bool   `yaml:"deprecated"`
}

func loadTopicsYAML(t *testing.T) topicsYAML {
	t.Helper()

	// Resolve path relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	yamlPath := filepath.Join(filepath.Dir(thisFile), "topics.yaml")

	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("failed to read topics.yaml: %v", err)
	}

	var parsed topicsYAML
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse topics.yaml: %v", err)
	}
	return parsed
}

func TestTopicsYAML_ParsesWithoutError(t *testing.T) {
	parsed := loadTopicsYAML(t)
	if len(parsed.Services) == 0 {
		t.Fatal("topics.yaml parsed with no services")
	}
}

func TestTopicsYAML_NoEmptyTopicNames(t *testing.T) {
	parsed := loadTopicsYAML(t)
	for svc, entry := range parsed.Services {
		for _, topic := range entry.Topics {
			if topic.Name == "" {
				t.Errorf("service %q has a topic with an empty name", svc)
			}
		}
	}
}

func TestTopicsYAML_ConsistentWithGoConstants(t *testing.T) {
	parsed := loadTopicsYAML(t)

	// Collect all non-deprecated topic names declared in topics.yaml.
	yamlTopics := make(map[string]bool)
	for _, entry := range parsed.Services {
		for _, topic := range entry.Topics {
			if topic.Name != "" && !topic.Deprecated {
				yamlTopics[topic.Name] = true
			}
		}
	}

	// Every Go constant in All() must have a corresponding entry in topics.yaml.
	for _, topic := range topics.All() {
		if !yamlTopics[topic] {
			t.Errorf("topic %q is in topics.All() but missing from topics.yaml", topic)
		}
	}

	// Every topic in topics.yaml must appear in topics.All().
	goTopics := make(map[string]bool, len(topics.All()))
	for _, topic := range topics.All() {
		goTopics[topic] = true
	}

	for topicName := range yamlTopics {
		if !goTopics[topicName] {
			t.Errorf("topic %q is in topics.yaml but missing from topics.All()", topicName)
		}
	}
}

func TestTopicsYAML_NoDuplicates(t *testing.T) {
	parsed := loadTopicsYAML(t)

	seen := make(map[string]string) // topic name -> service name
	for svcName, entry := range parsed.Services {
		for _, topic := range entry.Topics {
			if topic.Name == "" {
				continue // Empty names are caught by TestTopicsYAML_NoEmptyTopicNames
			}
			if prev, exists := seen[topic.Name]; exists {
				t.Errorf("duplicate topic %q: defined in both %q and %q", topic.Name, prev, svcName)
			}
			seen[topic.Name] = svcName
		}
	}
}

// TestAll_ContainsAllConstants verifies that All() returns exactly the set of
// canonical topic constants defined in this package. This prevents drift between
// the const block and the All() slice when new topics are added.
func TestAll_ContainsAllConstants(t *testing.T) {
	knownConstants := []string{
		topics.AuditEventsV1,
		topics.AuditEventsDLQV1,
		topics.CurrentAccountAccountFrozenV1,
		topics.CurrentAccountAccountUnfrozenV1,
		topics.CurrentAccountAccountClosedV1,
		topics.CurrentAccountWithdrawalStatusV1,
		topics.FinancialAccountingBookingLogControlledV1,
		topics.MarketInformationObservationRecordedV1,
		topics.PaymentOrderInitiatedV1,
		topics.PaymentOrderReservedV1,
		topics.PaymentOrderExecutingV1,
		topics.PaymentOrderCompletedV1,
		topics.PaymentOrderFailedV1,
		topics.PaymentOrderCancelledV1,
		topics.PaymentOrderReversedV1,
		topics.PositionKeepingTransactionCapturedV1,
		topics.PositionKeepingTransactionAmendedV1,
		topics.PositionKeepingTransactionReconciledV1,
		topics.PositionKeepingTransactionPostedV1,
		topics.PositionKeepingTransactionRejectedV1,
		topics.PositionKeepingTransactionFailedV1,
		topics.PositionKeepingTransactionCancelledV1,
		topics.PositionKeepingBulkTransactionCapturedV1,
		topics.PositionKeepingOpeningBalanceRecordedV1,
		topics.PartyCreatedV1,
		topics.PartyUpdatedV1,
		topics.PartyVerificationCompletedV1,
		topics.InternalAccountFacilityCreatedV1,
		topics.InternalAccountBookingCreatedV1,
		topics.ReconciliationRunStartedV1,
		topics.ReconciliationRunCompletedV1,
		topics.ReconciliationVarianceDetectedV1,
		topics.ReconciliationPositionLockRequestedV1,
		topics.ReconciliationDisputeCreatedV1,
		topics.ReconciliationDisputeResolvedV1,
	}

	allSet := make(map[string]bool, len(topics.All()))
	for _, topic := range topics.All() {
		allSet[topic] = true
	}

	// Every known constant must be in All().
	for _, c := range knownConstants {
		if !allSet[c] {
			t.Errorf("constant %q is missing from topics.All()", c)
		}
	}

	// All() must not contain any extras beyond the known constants.
	knownSet := make(map[string]bool, len(knownConstants))
	for _, c := range knownConstants {
		knownSet[c] = true
	}
	for _, topic := range topics.All() {
		if !knownSet[topic] {
			t.Errorf("topics.All() contains unexpected topic %q not in the known constants list", topic)
		}
	}

	// Count must match exactly.
	if len(topics.All()) != len(knownConstants) {
		t.Errorf("topics.All() has %d entries, expected %d", len(topics.All()), len(knownConstants))
	}
}
