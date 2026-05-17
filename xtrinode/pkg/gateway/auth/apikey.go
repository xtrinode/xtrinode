package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// APIKeyHeader is the HTTP header name for API key authentication
	//nolint:gosec // G101: header name constant; not a credential
	APIKeyHeader = "X-API-Key"

	// DefaultSecretPollInterval is the default interval for polling Secret changes
	DefaultSecretPollInterval = 10 * time.Second

	// MinAPIKeyLength is the minimum length for an API key
	MinAPIKeyLength = 16
)

// APIKeyAuthenticator implements API key authentication
// Supports multiple keys per keyID (comma-separated) for rotation
type APIKeyAuthenticator struct {
	client client.Client
	log    logr.Logger
	config *APIKeyConfig

	// In-memory cache of valid API keys
	// Maps API key -> keyID (e.g., "key-abc123" -> "service-1")
	// Supports multiple keys per keyID: "key-old,key-new" -> "service-1"
	validKeys map[string]string // key -> keyID
	keysLock  sync.RWMutex

	// Last reload time and resource version for change detection
	lastReload          time.Time
	lastResourceVersion string
}

// NewAPIKeyAuthenticator creates a new API key authenticator.
//
// The authenticator loads API keys from a Kubernetes Secret and watches for changes.
// Keys are stored in-memory for fast lookup during authentication.
//
// Parameters:
//   - cli: Kubernetes client for accessing Secrets
//   - log: Logger for structured logging
//   - config: API key configuration (must not be nil)
//
// Returns:
//   - *APIKeyAuthenticator: Initialized authenticator (not started)
//   - error: Error if configuration is invalid
//
// After creation, call Start() to begin watching for Secret changes.
func NewAPIKeyAuthenticator(cli client.Client, log logr.Logger, config *APIKeyConfig) (*APIKeyAuthenticator, error) {
	if config == nil {
		return nil, fmt.Errorf("API key config is required")
	}

	if config.SecretName == "" {
		return nil, fmt.Errorf("secret name is required")
	}

	if config.SecretKey == "" {
		return nil, fmt.Errorf("secret key is required")
	}

	if config.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}

	auth := &APIKeyAuthenticator{
		client:    cli,
		log:       log,
		config:    config,
		validKeys: make(map[string]string),
	}

	return auth, nil
}

// Start initializes the authenticator and starts watching for Secret changes.
//
// This function:
//   - Loads initial API keys from the configured Secret
//   - Starts a background goroutine that polls for Secret changes
//   - Reloads keys automatically when Secret is updated
//
// Parameters:
//   - ctx: Context for cancellation. When context is canceled, the watcher stops.
//
// Returns:
//   - error: Error if initial key loading fails
//
// The watcher runs until ctx is canceled. Keys are reloaded every DefaultSecretPollInterval.
func (a *APIKeyAuthenticator) Start(ctx context.Context) error {
	// Load initial keys
	if err := a.reloadSecret(ctx); err != nil {
		return fmt.Errorf("failed to load initial API keys: %w", err)
	}

	// Start watching for Secret changes
	go a.watchSecret(ctx)

	return nil
}

// Authenticate validates the API key from the request header.
//
// The function:
//   - Extracts API key from X-API-Key header
//   - Validates key length (minimum MinAPIKeyLength)
//   - Looks up key in in-memory cache
//   - Returns authentication result
//
// Parameters:
//   - r: HTTP request containing X-API-Key header
//
// Returns:
//   - *Result: Authentication result with Authenticated=true if key is valid
//   - error: Should not return error for invalid keys (returns Result with Authenticated=false)
//
// Thread-safe: Uses read lock for concurrent access to key cache.
func (a *APIKeyAuthenticator) Authenticate(r *http.Request) (*Result, error) {
	// Extract API key from header
	apiKey := r.Header.Get(APIKeyHeader)
	if apiKey == "" {
		return &Result{Authenticated: false}, nil
	}

	// Trim whitespace
	apiKey = strings.TrimSpace(apiKey)

	// Validate key length
	if len(apiKey) < MinAPIKeyLength {
		a.log.V(1).Info("API key too short", "length", len(apiKey))
		return &Result{Authenticated: false}, nil
	}

	// Compare against loaded keys without using the presented secret as a map lookup key.
	a.keysLock.RLock()
	keyID := ""
	matched := 0
	for validKey, candidateKeyID := range a.validKeys {
		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(validKey)) == 1 {
			keyID = candidateKeyID
			matched = 1
		}
	}
	a.keysLock.RUnlock()

	if matched != 1 {
		a.log.V(1).Info("Invalid API key", "keyPrefix", maskKey(apiKey))
		return &Result{Authenticated: false}, nil
	}

	// Authentication successful
	a.log.V(1).Info("API key authenticated", "keyID", keyID)
	return &Result{
		Authenticated: true,
		KeyID:         keyID,
		User:          keyID, // For API key, user is same as keyID
		Metadata:      map[string]string{"auth_type": "api-key"},
	}, nil
}

// Reload reloads API keys from the Secret
// This is called automatically by watchSecret, but can be called manually for testing
func (a *APIKeyAuthenticator) Reload(ctx context.Context) error {
	return a.reloadSecret(ctx)
}

// reloadSecret loads API keys from Kubernetes Secret
func (a *APIKeyAuthenticator) reloadSecret(ctx context.Context) error {
	secret := &corev1.Secret{}
	err := a.client.Get(ctx, types.NamespacedName{
		Name:      a.config.SecretName,
		Namespace: a.config.Namespace,
	}, secret)

	if err != nil {
		return fmt.Errorf("failed to get secret %s/%s: %w", a.config.Namespace, a.config.SecretName, err)
	}

	// Check if Secret has changed
	// If lastResourceVersion is empty, this is the first load
	if a.lastResourceVersion != "" && secret.ResourceVersion == a.lastResourceVersion {
		return nil // No changes
	}

	// Get API keys data
	keysData, exists := secret.Data[a.config.SecretKey]
	if !exists {
		return fmt.Errorf("secret key %s not found in secret %s/%s", a.config.SecretKey, a.config.Namespace, a.config.SecretName)
	}

	// Parse API keys
	newKeys, err := parseAPIKeys(string(keysData))
	if err != nil {
		return fmt.Errorf("failed to parse API keys: %w", err)
	}

	// Update in-memory cache
	a.keysLock.Lock()
	a.validKeys = newKeys
	a.lastReload = time.Now()
	a.lastResourceVersion = secret.ResourceVersion
	a.keysLock.Unlock()

	a.log.Info("Reloaded API keys", "count", len(newKeys), "keyIDs", getKeyIDs(newKeys))

	return nil
}

// watchSecret watches for Secret changes and reloads keys
func (a *APIKeyAuthenticator) watchSecret(ctx context.Context) {
	ticker := time.NewTicker(DefaultSecretPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.reloadSecret(ctx); err != nil {
				a.log.V(1).Info("Failed to reload secret", "error", err)
			}
		}
	}
}

// parseAPIKeys parses API keys from Secret data
// Format: keyID: "api-key-value" or keyID: "key1,key2" (for rotation)
// Example:
//
//	service-1: "key-abc123"
//	service-2: "key-def456,key-xyz789"
func parseAPIKeys(data string) (map[string]string, error) {
	keys := make(map[string]string)

	if data == "" {
		return keys, nil
	}

	lines := strings.Split(data, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		// Parse format: keyID: "key-value" or keyID: "key1,key2"
		// Handle both "keyID: value" and "keyID:value" formats
		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue // Skip invalid lines (no colon)
		}

		keyID := strings.TrimSpace(line[:colonIdx])
		keyValue := strings.TrimSpace(line[colonIdx+1:])

		if keyID == "" || keyValue == "" {
			continue // Skip empty keyID or keyValue
		}

		// Remove quotes if present (both single and double)
		keyValue = strings.Trim(keyValue, `"'`)

		if keyID == "" || keyValue == "" {
			continue
		}

		// Handle multiple keys per keyID (comma-separated for rotation)
		keyList := strings.Split(keyValue, ",")
		for _, key := range keyList {
			key = strings.TrimSpace(key)
			if key != "" && len(key) >= MinAPIKeyLength {
				keys[key] = keyID
			}
		}
	}

	return keys, nil
}

// getKeyIDs extracts unique keyIDs from the keys map
func getKeyIDs(keys map[string]string) []string {
	keyIDSet := make(map[string]bool)
	for _, keyID := range keys {
		keyIDSet[keyID] = true
	}

	keyIDs := make([]string, 0, len(keyIDSet))
	for keyID := range keyIDSet {
		keyIDs = append(keyIDs, keyID)
	}
	return keyIDs
}

// maskKey masks an API key for logging (shows first 4 and last 4 characters)
func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
