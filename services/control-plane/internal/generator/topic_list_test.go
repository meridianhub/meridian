package generator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildTopicList_ContainsHeader(t *testing.T) {
	result := BuildTopicList()

	assert.Contains(t, result, "## Available Event Topics (for saga trigger: \"event:\")")
	assert.Contains(t, result, "trigger: \"event:<topic-name>\"")
}

func TestBuildTopicList_ContainsKnownTopics(t *testing.T) {
	result := BuildTopicList()

	// Known topics from the topics package
	assert.Contains(t, result, "position-keeping.transaction-captured.v1")
	assert.Contains(t, result, "payment-order.initiated.v1")
	assert.Contains(t, result, "current-account.account-frozen.v1")
	assert.Contains(t, result, "audit.events.v1")
}

func TestBuildTopicList_GroupedByService(t *testing.T) {
	result := BuildTopicList()

	// Should have service group headers
	assert.Contains(t, result, "### position-keeping")
	assert.Contains(t, result, "### payment-order")
	assert.Contains(t, result, "### current-account")
	assert.Contains(t, result, "### audit")
}

func TestBuildTopicList_TopicsFormattedAsListItems(t *testing.T) {
	result := BuildTopicList()

	// Each topic should be formatted as a markdown list item with backtick name
	assert.Contains(t, result, "- `position-keeping.transaction-captured.v1`")
}

func TestBuildTopicList_ServicesSortedAlphabetically(t *testing.T) {
	result := BuildTopicList()

	// audit should come before position-keeping alphabetically
	auditIdx := strings.Index(result, "### audit")
	pkIdx := strings.Index(result, "### position-keeping")
	assert.Less(t, auditIdx, pkIdx, "services should be sorted alphabetically")
}

func TestBuildTopicList_TopicsHaveDescriptions(t *testing.T) {
	result := BuildTopicList()

	// Each topic line should have a description after " — "
	lines := strings.Split(result, "\n")
	topicLines := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "- `") {
			assert.Contains(t, line, " — ", "topic line should have description: %s", line)
			topicLines++
		}
	}
	assert.Greater(t, topicLines, 0, "should have at least one topic line")
}

func TestBuildTopicList_NonZeroTopics(t *testing.T) {
	result := BuildTopicList()

	// Count topic lines
	count := strings.Count(result, "\n- `")
	assert.Greater(t, count, 10, "should contain more than 10 topics")
}

func TestTopicServicePrefix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "StandardTopic", input: "position-keeping.transaction-captured.v1", expected: "position-keeping"},
		{name: "AuditTopic", input: "audit.events.v1", expected: "audit"},
		{name: "NoDot", input: "standalone", expected: "standalone"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := topicServicePrefix(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDescribeTopicName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "TransactionCaptured",
			input:    "position-keeping.transaction-captured.v1",
			contains: "Transaction Captured",
		},
		{
			name:     "AccountFrozen",
			input:    "current-account.account-frozen.v1",
			contains: "Account Frozen",
		},
		{
			name:     "PaymentInitiated",
			input:    "payment-order.initiated.v1",
			contains: "Initiated",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := describeTopicName(tc.input)
			assert.Contains(t, result, tc.contains)
		})
	}
}
