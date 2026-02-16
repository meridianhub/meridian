package saga

import (
	"errors"
	"fmt"
	"strings"
)

// CompositeAccountRef represents a parsed composite account reference
// in the format: party:<party_id>:org:<org_id>:currency:<code>
type CompositeAccountRef struct {
	PartyID  string
	OrgID    string
	Currency string
}

// ErrMalformedCompositeRef is returned when a composite reference has an invalid format.
var ErrMalformedCompositeRef = errors.New("malformed composite account reference")

// compositeRefSegmentCount is the expected number of segments in a composite reference.
// Format: party:<party_id>:org:<org_id>:currency:<code> = 6 segments.
const compositeRefSegmentCount = 6

// BuildCompositeAccountRef constructs a properly formatted composite account reference.
// Format: party:<party_id>:org:<org_id>:currency:<code>
func BuildCompositeAccountRef(partyID, orgID, currency string) string {
	return fmt.Sprintf("party:%s:org:%s:currency:%s", partyID, orgID, currency)
}

// ParseCompositeAccountRef parses a composite account reference string into its components.
// Expected format: party:<party_id>:org:<org_id>:currency:<code>
// Returns ErrMalformedCompositeRef if the format is invalid.
func ParseCompositeAccountRef(ref string) (*CompositeAccountRef, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != compositeRefSegmentCount {
		return nil, fmt.Errorf("%w: expected %d segments, got %d in %q",
			ErrMalformedCompositeRef, compositeRefSegmentCount, len(parts), ref)
	}

	if parts[0] != "party" {
		return nil, fmt.Errorf("%w: expected segment 1 to be \"party\", got %q", ErrMalformedCompositeRef, parts[0])
	}
	if parts[2] != "org" {
		return nil, fmt.Errorf("%w: expected segment 3 to be \"org\", got %q", ErrMalformedCompositeRef, parts[2])
	}
	if parts[4] != "currency" {
		return nil, fmt.Errorf("%w: expected segment 5 to be \"currency\", got %q", ErrMalformedCompositeRef, parts[4])
	}

	partyID := parts[1]
	orgID := parts[3]
	currency := parts[5]

	if partyID == "" {
		return nil, fmt.Errorf("%w: party_id must not be empty", ErrMalformedCompositeRef)
	}
	if orgID == "" {
		return nil, fmt.Errorf("%w: org_id must not be empty", ErrMalformedCompositeRef)
	}
	if currency == "" {
		return nil, fmt.Errorf("%w: currency must not be empty", ErrMalformedCompositeRef)
	}

	return &CompositeAccountRef{
		PartyID:  partyID,
		OrgID:    orgID,
		Currency: currency,
	}, nil
}

// IsCompositeAccountRef returns true if the reference contains colons,
// indicating it may be a composite reference format.
func IsCompositeAccountRef(ref string) bool {
	return strings.Contains(ref, ":")
}
