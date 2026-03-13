package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	// clientIDBytes is the number of random bytes in a generated client ID.
	clientIDBytes = 16
	// registryMaxClients caps the number of registered clients to prevent
	// memory exhaustion from unauthenticated registration requests.
	registryMaxClients = 10000
	// registryEvictInterval is how often the registry sweeps expired entries.
	registryEvictInterval = 10 * time.Minute
	// clientTTL is how long a dynamically registered client remains valid.
	// MCP clients must re-register after this period.
	clientTTL = 24 * time.Hour
	// registrationBodyLimit is the maximum request body size for /oauth/register.
	registrationBodyLimit = 64 << 10 // 64 KiB

	// Supported OAuth 2.1 values for MCP.
	supportedGrantType    = "authorization_code"
	supportedResponseType = "code"
	supportedAuthMethod   = "none"
)

var (
	errRegistryFull      = errors.New("client registry is full")
	errNoRedirectURIs    = errors.New("redirect_uris is required")
	errInvalidRedirectFn = errors.New("redirect_uri is not allowed")
)

// RegisteredClient holds metadata for a dynamically registered OAuth client.
type RegisteredClient struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	registeredAt            time.Time
}

// ClientRegistry is a thread-safe in-memory store for dynamically registered
// OAuth clients (RFC 7591). Registrations are ephemeral and expire after clientTTL.
type ClientRegistry struct {
	mu        sync.RWMutex
	clients   map[string]RegisteredClient
	stop      chan struct{}
	closeOnce sync.Once
}

// NewClientRegistry creates an empty registry and starts background eviction.
func NewClientRegistry() *ClientRegistry {
	r := &ClientRegistry{
		clients: make(map[string]RegisteredClient),
		stop:    make(chan struct{}),
	}
	go r.evictLoop()
	return r
}

// Close stops the background eviction goroutine.
func (r *ClientRegistry) Close() {
	r.closeOnce.Do(func() { close(r.stop) })
}

func (r *ClientRegistry) evictLoop() {
	ticker := time.NewTicker(registryEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.evictExpired()
		case <-r.stop:
			return
		}
	}
}

func (r *ClientRegistry) evictExpired() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, client := range r.clients {
		if time.Since(client.registeredAt) > clientTTL {
			delete(r.clients, id)
		}
	}
}

// Register adds a new client and returns the generated client ID.
func (r *ClientRegistry) Register(client RegisteredClient) (RegisteredClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Evict expired clients before enforcing the cap so that stale entries
	// don't block legitimate registrations between background sweeps.
	now := time.Now()
	for id, existing := range r.clients {
		if now.Sub(existing.registeredAt) > clientTTL {
			delete(r.clients, id)
		}
	}

	if len(r.clients) >= registryMaxClients {
		return RegisteredClient{}, errRegistryFull
	}

	id, err := generateClientID()
	if err != nil {
		return RegisteredClient{}, fmt.Errorf("generate client ID: %w", err)
	}

	client.ClientID = id
	client.registeredAt = now
	r.clients[id] = client
	return client, nil
}

// Lookup returns a registered client by ID.
func (r *ClientRegistry) Lookup(clientID string) (RegisteredClient, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[clientID]
	if !ok {
		return RegisteredClient{}, false
	}
	if time.Since(client.registeredAt) > clientTTL {
		return RegisteredClient{}, false
	}
	return client, true
}

// HasRedirectURI checks if the given redirect URI is registered for the client.
func (c RegisteredClient) HasRedirectURI(uri string) bool {
	for _, u := range c.RedirectURIs {
		if u == uri {
			return true
		}
	}
	return false
}

// generateClientID returns a random hex-encoded client identifier.
func generateClientID() (string, error) {
	b := make([]byte, clientIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate client ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// registrationRequest is the RFC 7591 client registration request body.
type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// validateMetadata validates and defaults the registration metadata fields.
// Returns the resolved values and an error description if validation fails.
func (req registrationRequest) validateMetadata() (grantTypes, responseTypes []string, authMethod, errDesc string) {
	grantTypes = req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{supportedGrantType}
	} else if len(grantTypes) != 1 || grantTypes[0] != supportedGrantType {
		return nil, nil, "", "unsupported grant_types: only authorization_code is supported"
	}

	responseTypes = req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{supportedResponseType}
	} else if len(responseTypes) != 1 || responseTypes[0] != supportedResponseType {
		return nil, nil, "", "unsupported response_types: only code is supported"
	}

	authMethod = req.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = supportedAuthMethod
	} else if authMethod != supportedAuthMethod {
		return nil, nil, "", "unsupported token_endpoint_auth_method: only none is supported"
	}

	return grantTypes, responseTypes, authMethod, ""
}

// RegistrationHandler handles POST /oauth/register for dynamic client
// registration per RFC 7591.
type RegistrationHandler struct {
	registry *ClientRegistry
	logger   *slog.Logger
}

// NewRegistrationHandler creates a new RegistrationHandler.
func NewRegistrationHandler(registry *ClientRegistry, logger *slog.Logger) *RegistrationHandler {
	return &RegistrationHandler{
		registry: registry,
		logger:   logger,
	}
}

// ServeHTTP implements http.Handler.
func (h *RegistrationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req registrationRequest
	r.Body = http.MaxBytesReader(w, r.Body, registrationBodyLimit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	// Reject trailing data after the JSON object.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if len(req.RedirectURIs) == 0 {
		writeRegistrationError(w, errNoRedirectURIs.Error())
		return
	}

	for _, uri := range req.RedirectURIs {
		if !isAllowedRedirectURI(uri) {
			writeRegistrationError(w, fmt.Sprintf("%s: %s", errInvalidRedirectFn.Error(), uri))
			return
		}
	}

	grantTypes, responseTypes, authMethod, errDesc := req.validateMetadata()
	if errDesc != "" {
		writeRegistrationError(w, errDesc)
		return
	}

	client, err := h.registry.Register(RegisteredClient{
		ClientName:              req.ClientName,
		RedirectURIs:            req.RedirectURIs,
		GrantTypes:              grantTypes,
		ResponseTypes:           responseTypes,
		TokenEndpointAuthMethod: authMethod,
	})
	if err != nil {
		if errors.Is(err, errRegistryFull) {
			h.logger.Warn("client registry full, rejecting registration")
			http.Error(w, "too many registered clients", http.StatusServiceUnavailable)
			return
		}
		h.logger.Error("client registration failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("dynamic client registered",
		"client_id", client.ClientID,
		"client_name", client.ClientName)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(client)
}

func writeRegistrationError(w http.ResponseWriter, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             "invalid_client_metadata",
		"error_description": description,
	})
}
