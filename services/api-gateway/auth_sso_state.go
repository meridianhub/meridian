package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ErrStateStoreFull is returned when the state store has reached its maximum
// capacity and cannot accept new entries. This prevents memory exhaustion
// from unauthenticated SSO initiation requests.
var ErrStateStoreFull = errors.New("state store: capacity limit reached")

// StateData holds the PKCE and tenant context for an in-flight SSO authorization.
type StateData struct {
	CodeVerifier string
	TenantID     tenant.TenantID
	TenantSlug   string // subdomain slug for JWT claims (may differ from TenantID)
	ReturnURL    string
}

// stateEntry pairs data with an expiry time.
type stateEntry struct {
	data      StateData
	expiresAt time.Time
}

// StateStore is a thread-safe in-memory TTL cache for PKCE state parameters.
// Entries are automatically removed on Get (one-time use) and lazily purged
// on Set when more than maxLazyPurge expired entries have accumulated.
//
// Limitation: In-memory storage only works with a single gateway replica. With
// horizontal scaling, the initiate and callback requests may hit different
// replicas. For multi-replica deployments, swap this for a shared store (Redis,
// CockroachDB, or signed encrypted state tokens). The injectable StateStore
// design makes this straightforward.
type StateStore struct {
	mu    sync.Mutex
	items map[string]stateEntry
	ttl   time.Duration
	now   func() time.Time // pluggable clock for tests
}

const (
	defaultStateTTL = 5 * time.Minute
	maxLazyPurge    = 100
	maxStoreEntries = 10000 // hard cap to prevent memory exhaustion from unauthenticated requests
	stateKeyBytes   = 16    // 128-bit random state keys
)

// NewStateStore creates a StateStore with the given TTL.
// If ttl is zero, defaults to 5 minutes.
func NewStateStore(ttl time.Duration) *StateStore {
	if ttl == 0 {
		ttl = defaultStateTTL
	}
	return &StateStore{
		items: make(map[string]stateEntry),
		ttl:   ttl,
		now:   time.Now,
	}
}

// Set stores state data under a new random key and returns the key.
func (s *StateStore) Set(data StateData) (string, error) {
	key, err := generateRandomHex(stateKeyBytes)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Purge expired entries if approaching capacity to make room.
	if len(s.items) >= maxLazyPurge {
		s.purgeExpiredLocked()
	}

	// Hard cap: reject new entries if the store is full after purging.
	// This prevents memory exhaustion from unauthenticated SSO initiation flood.
	if len(s.items) >= maxStoreEntries {
		return "", ErrStateStoreFull
	}

	s.items[key] = stateEntry{
		data:      data,
		expiresAt: s.now().Add(s.ttl),
	}

	return key, nil
}

// Get retrieves and deletes state data for the given key (one-time use).
// Returns the data and true if found and not expired, or zero value and false otherwise.
func (s *StateStore) Get(key string) (StateData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.items[key]
	if !ok {
		return StateData{}, false
	}
	delete(s.items, key)

	if s.now().After(entry.expiresAt) {
		return StateData{}, false
	}

	return entry.data, true
}

// Len returns the number of entries (including expired, not yet purged).
func (s *StateStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// purgeExpiredLocked removes all expired entries. Caller must hold s.mu.
func (s *StateStore) purgeExpiredLocked() {
	now := s.now()
	for k, v := range s.items {
		if now.After(v.expiresAt) {
			delete(s.items, k)
		}
	}
}

// generateRandomHex returns a hex-encoded random string of n bytes.
func generateRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
