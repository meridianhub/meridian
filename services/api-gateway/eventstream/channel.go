package eventstream

// DeriveChannel converts a Kafka topic to a logical channel name by stripping
// trailing version suffixes of the form ".vN" where N is one or more digits.
//
// Examples:
//
//	"payment-order.reserved.v1"          → "payment-order.reserved"
//	"audit.events.current-account.v1"    → "audit.events.current-account"
//	"payment-order.reserved"             → "payment-order.reserved"
//	"events"                             → "events"
//
// Returns ErrEmptyTopic if topic is empty.
func DeriveChannel(topic string) (string, error) {
	if topic == "" {
		return "", ErrEmptyTopic
	}
	return deriveChannel(topic), nil
}

// ValidateChannelPattern checks that a channel pattern is non-empty and that
// any wildcard character appears only as the last character.
//
// Returns ErrEmptyChannelPattern if pattern is empty.
// Returns ErrInvalidChannelPattern if the wildcard is not in trailing position.
func ValidateChannelPattern(pattern string) error {
	return validateChannelPattern(ChannelPattern(pattern))
}

// ChannelMatcher holds a set of ChannelPattern values and can test whether a
// given channel name satisfies at least one of them.
//
// The zero value is usable and matches nothing. Use NewChannelMatcher or
// NewChannelMatcherFromSubscription to construct with an initial set of patterns.
//
// ChannelMatcher is not safe for concurrent use. Callers that share a
// ChannelMatcher across goroutines must synchronize all calls to Add and
// Matches themselves.
type ChannelMatcher struct {
	patterns []ChannelPattern
}

// NewChannelMatcher returns a ChannelMatcher initialized with the given patterns.
func NewChannelMatcher(patterns ...ChannelPattern) *ChannelMatcher {
	p := make([]ChannelPattern, len(patterns))
	copy(p, patterns)
	return &ChannelMatcher{patterns: p}
}

// NewChannelMatcherFromSubscription returns a ChannelMatcher seeded from the
// channel patterns in the given Subscription.
func NewChannelMatcherFromSubscription(sub Subscription) *ChannelMatcher {
	return NewChannelMatcher(sub.Channels...)
}

// Add appends a pattern to the matcher. Duplicate patterns are retained but
// harmless — Matches short-circuits on the first hit.
func (m *ChannelMatcher) Add(pattern ChannelPattern) {
	m.patterns = append(m.patterns, pattern)
}

// Matches reports whether channel satisfies at least one of the stored patterns.
// Returns false when no patterns have been added.
func (m *ChannelMatcher) Matches(channel string) bool {
	for _, p := range m.patterns {
		if p.Matches(channel) {
			return true
		}
	}
	return false
}
