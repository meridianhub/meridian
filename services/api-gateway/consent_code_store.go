package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// consentCodeTTL is how long a consent code remains valid.
	consentCodeTTL = 2 * time.Minute
	// consentCodeEvictInterval is how often the store sweeps expired entries.
	consentCodeEvictInterval = 1 * time.Minute
	// consentCodeMaxEntries caps entries to prevent memory exhaustion.
	consentCodeMaxEntries = 10_000
	// consentCodeBytes is the number of random bytes in a generated consent code.
	consentCodeBytes = 32
)

var errConsentCodeStoreFull = errors.New("consent code store is full")

// ConsentCodeEntry holds the state stored alongside a consent code.
type ConsentCodeEntry struct {
	Email          string
	TenantID       string   // UUID from JWT x-tenant-id claim
	TenantSlug     string   // subdomain slug for cross-validation
	MCPState       string   // key into OIDCStateStore
	ClientID       string   // OAuth client_id
	ApprovedScopes []string // e.g., ["mcp:default"]
	CreatedAt      time.Time
}

// ConsentCodeStore is a thread-safe in-memory store for consent codes.
// Each code can be consumed exactly once and expires after consentCodeTTL.
type ConsentCodeStore struct {
	mu        sync.Mutex
	entries   map[string]ConsentCodeEntry
	stop      chan struct{}
	closeOnce sync.Once
}

// NewConsentCodeStore creates an empty ConsentCodeStore and starts the
// background eviction goroutine. Call [ConsentCodeStore.Close] to stop it.
func NewConsentCodeStore() *ConsentCodeStore {
	s := &ConsentCodeStore{
		entries: make(map[string]ConsentCodeEntry),
		stop:    make(chan struct{}),
	}
	go s.evictLoop()
	return s
}

// Close stops the background eviction goroutine. Safe to call multiple times.
func (s *ConsentCodeStore) Close() {
	s.closeOnce.Do(func() { close(s.stop) })
}

func (s *ConsentCodeStore) evictLoop() {
	ticker := time.NewTicker(consentCodeEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evictExpired()
		case <-s.stop:
			return
		}
	}
}

func (s *ConsentCodeStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for code, entry := range s.entries {
		if time.Since(entry.CreatedAt) > consentCodeTTL {
			delete(s.entries, code)
		}
	}
}

// Store saves a consent code entry and returns the generated code.
// Returns errConsentCodeStoreFull if the store has reached its capacity limit.
func (s *ConsentCodeStore) Store(entry ConsentCodeEntry) (string, error) {
	entry.CreatedAt = time.Now()
	entry.ApprovedScopes = append([]string(nil), entry.ApprovedScopes...)

	code, err := generateConsentCode()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= consentCodeMaxEntries {
		return "", errConsentCodeStoreFull
	}
	s.entries[code] = entry
	return code, nil
}

// Consume atomically retrieves and deletes a consent code.
// Returns (entry, true) if the code exists and has not expired.
// Returns (zero, false) if the code is unknown or expired.
func (s *ConsentCodeStore) Consume(code string) (ConsentCodeEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[code]
	if !ok {
		return ConsentCodeEntry{}, false
	}

	// Always delete (one-time use), even if expired.
	delete(s.entries, code)

	if time.Since(entry.CreatedAt) > consentCodeTTL {
		return ConsentCodeEntry{}, false
	}

	return entry, true
}

// generateConsentCode returns a cryptographically random, URL-safe consent code.
func generateConsentCode() (string, error) {
	b := make([]byte, consentCodeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate consent code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
