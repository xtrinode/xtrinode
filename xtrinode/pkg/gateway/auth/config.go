package auth

import "time"

// Config holds authentication configuration
type Config struct {
	// Enabled indicates if authentication is enabled
	Enabled bool

	// Type specifies the authentication type ("api-key", "oauth", "oidc", "bearer-token", "jwt", "none")
	Type string

	// APIKey configures API key authentication
	APIKey *APIKeyConfig

	// BearerToken configures OAuth/OIDC Bearer token/JWT authentication
	BearerToken *BearerTokenConfig
}

// APIKeyConfig holds API key authentication configuration
type APIKeyConfig struct {
	// SecretName is the name of the Kubernetes Secret containing API keys
	SecretName string

	// SecretKey is the key in the Secret that contains the API keys data
	SecretKey string

	// Namespace is the namespace where the Secret is located
	Namespace string
}

// BearerTokenConfig holds Bearer token/JWT authentication configuration
type BearerTokenConfig struct {
	// Issuer is the OIDC issuer URL (for OIDC validation)
	Issuer string

	// Audience is the expected audience claim value
	Audience string

	// JWKSUrl is the JWKS endpoint URL (for OIDC validation)
	JWKSUrl string

	// SecretName is the name of the Kubernetes Secret containing JWT secret (for shared secret validation)
	SecretName string

	// SecretKey is the key in the Secret that contains the JWT secret
	SecretKey string

	// Namespace is the namespace where the Secret is located
	Namespace string

	// RefreshInterval is how often JWKS keys are refreshed
	RefreshInterval time.Duration
}
