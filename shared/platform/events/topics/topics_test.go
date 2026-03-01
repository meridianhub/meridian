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
	Name     string `yaml:"name"`
	Constant string `yaml:"constant"`
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

	// Collect all topic names declared in topics.yaml.
	yamlTopics := make(map[string]bool)
	for _, entry := range parsed.Services {
		for _, topic := range entry.Topics {
			if topic.Name != "" {
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
			if prev, exists := seen[topic.Name]; exists {
				t.Errorf("duplicate topic %q: defined in both %q and %q", topic.Name, prev, svcName)
			}
			seen[topic.Name] = svcName
		}
	}
}
