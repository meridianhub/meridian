package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// BuildTopicList generates a formatted list of all available Kafka event topics.
// Topics are grouped by service domain. The output is optimized for LLM consumption
// when generating saga triggers that use the "event:" prefix.
func BuildTopicList() string {
	var sb strings.Builder

	sb.WriteString("## Available Event Topics (for saga trigger: \"event:\")\n\n")
	sb.WriteString("Use these topics as saga triggers: `trigger: \"event:<topic-name>\"`\n\n")

	// Group topics by service domain (prefix before the first dot)
	byService := make(map[string][]string)
	for _, topic := range topics.All() {
		svc := topicServicePrefix(topic)
		byService[svc] = append(byService[svc], topic)
	}

	// Sort service names for deterministic output
	services := make([]string, 0, len(byService))
	for svc := range byService {
		services = append(services, svc)
	}
	sort.Strings(services)

	for _, svc := range services {
		topicList := byService[svc]
		sort.Strings(topicList)

		fmt.Fprintf(&sb, "### %s\n\n", svc)
		for _, topic := range topicList {
			desc := describeTopicName(topic)
			fmt.Fprintf(&sb, "- `%s` - %s\n", topic, desc)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// topicServicePrefix extracts the service domain from a topic name.
// Topics follow the pattern: <service>.<event-name>.<version>
// For "position-keeping.transaction-captured.v1" returns "position-keeping".
func topicServicePrefix(topic string) string {
	if idx := strings.Index(topic, "."); idx >= 0 {
		return topic[:idx]
	}
	return topic
}

// describeTopicName derives a human-readable description from a topic name.
// Strips the version suffix and converts kebab-case to title-case words.
func describeTopicName(topic string) string {
	parts := strings.SplitN(topic, ".", 3)
	if len(parts) < 2 {
		return topic
	}

	// Use the event name part (index 1), strip version (index 2)
	eventPart := parts[1]

	// Convert kebab-case event name to readable words
	words := strings.Split(eventPart, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}

	service := parts[0]
	serviceWords := strings.Split(service, "-")
	for i, w := range serviceWords {
		if len(w) > 0 {
			serviceWords[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}

	return fmt.Sprintf("%s %s event", strings.Join(serviceWords, " "), strings.Join(words, " "))
}
