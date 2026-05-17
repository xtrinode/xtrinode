package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

const (
	defaultJWKSRefreshInterval = time.Hour
	defaultJWTClockSkew        = time.Minute
	maxOAuthHTTPResponseBytes  = 2 * 1024 * 1024
)

type BearerTokenAuthenticator struct {
	log        logr.Logger
	config     *BearerTokenConfig
	httpClient *http.Client

	keysLock sync.RWMutex
	keys     map[string]*rsa.PublicKey
	jwksURL  string
}

type oidcDiscoveryDocument struct {
	JWKSURI string `json:"jwks_uri"`
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Issuer            string        `json:"iss"`
	Subject           string        `json:"sub"`
	Audience          audienceClaim `json:"aud"`
	ExpiresAt         int64         `json:"exp"`
	NotBefore         int64         `json:"nbf"`
	IssuedAt          int64         `json:"iat"`
	Email             string        `json:"email"`
	PreferredUsername string        `json:"preferred_username"`
	Name              string        `json:"name"`
}

type audienceClaim []string

func (a *audienceClaim) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = []string{single}
		return nil
	}

	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}
	*a = multiple
	return nil
}

func NewBearerTokenAuthenticator(log logr.Logger, cfg *BearerTokenConfig) (*BearerTokenAuthenticator, error) {
	if cfg == nil {
		return nil, errors.New("bearer token config is required")
	}
	if cfg.Issuer == "" && cfg.JWKSUrl == "" {
		return nil, errors.New("auth-oauth-issuer or auth-oauth-jwks-url is required")
	}
	if strings.TrimSpace(cfg.Audience) == "" {
		return nil, errors.New("auth-oauth-audience is required")
	}
	if cfg.Issuer != "" {
		if err := validateOAuthHTTPURL(cfg.Issuer); err != nil {
			return nil, fmt.Errorf("invalid auth-oauth-issuer: %w", err)
		}
	}
	if cfg.JWKSUrl != "" {
		if err := validateOAuthHTTPURL(cfg.JWKSUrl); err != nil {
			return nil, fmt.Errorf("invalid auth-oauth-jwks-url: %w", err)
		}
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = defaultJWKSRefreshInterval
	}

	return &BearerTokenAuthenticator{
		log:    log,
		config: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		keys: make(map[string]*rsa.PublicKey),
	}, nil
}

func (a *BearerTokenAuthenticator) Start(ctx context.Context) error {
	if err := a.refreshKeys(ctx); err != nil {
		return err
	}

	go func() {
		ticker := time.NewTicker(a.config.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := a.refreshKeys(ctx); err != nil {
					a.log.Error(err, "failed to refresh OAuth JWKS")
				}
			}
		}
	}()

	return nil
}

func (a *BearerTokenAuthenticator) Authenticate(r *http.Request) (*Result, error) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return &Result{Authenticated: false}, nil
	}
	if !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return &Result{Authenticated: false}, nil
	}

	token := strings.TrimSpace(authHeader[len("Bearer "):])
	claims, err := a.validateJWT(r.Context(), token)
	if err != nil {
		a.log.V(1).Info("OAuth bearer token validation failed", "error", err)
		return &Result{Authenticated: false}, nil
	}

	user := claims.PreferredUsername
	if user == "" {
		user = claims.Email
	}
	if user == "" {
		user = claims.Subject
	}

	return &Result{
		Authenticated: true,
		KeyID:         claims.Subject,
		User:          user,
		Metadata: map[string]string{
			"auth_type": "oauth",
			"issuer":    claims.Issuer,
		},
	}, nil
}

func (a *BearerTokenAuthenticator) validateJWT(ctx context.Context, token string) (*jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("JWT must have three parts")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode JWT header: %w", err)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT claims: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode JWT signature: %w", err)
	}

	var header jwtHeader
	if parseErr := json.Unmarshal(headerBytes, &header); parseErr != nil {
		return nil, fmt.Errorf("parse JWT header: %w", parseErr)
	}
	if header.Kid == "" {
		return nil, errors.New("JWT kid is required")
	}

	var claims jwtClaims
	if parseErr := json.Unmarshal(claimsBytes, &claims); parseErr != nil {
		return nil, fmt.Errorf("parse JWT claims: %w", parseErr)
	}
	if claimsErr := a.validateClaims(&claims); claimsErr != nil {
		return nil, claimsErr
	}

	key := a.getKey(header.Kid)
	if key == nil {
		if refreshErr := a.refreshKeys(ctx); refreshErr != nil {
			return nil, fmt.Errorf("refresh JWKS after unknown kid: %w", refreshErr)
		}
		key = a.getKey(header.Kid)
		if key == nil {
			return nil, fmt.Errorf("no JWKS key found for kid %q", header.Kid)
		}
	}

	hash, hashed, err := hashJWTSigningInput(header.Alg, parts[0]+"."+parts[1])
	if err != nil {
		return nil, err
	}
	if verifyErr := rsa.VerifyPKCS1v15(key, hash, hashed, signature); verifyErr != nil {
		return nil, fmt.Errorf("verify JWT signature: %w", verifyErr)
	}

	return &claims, nil
}

func (a *BearerTokenAuthenticator) validateClaims(claims *jwtClaims) error {
	if a.config.Issuer != "" && claims.Issuer != strings.TrimRight(a.config.Issuer, "/") {
		return fmt.Errorf("issuer mismatch: got %q", claims.Issuer)
	}
	if a.config.Audience != "" && !claims.Audience.Contains(a.config.Audience) {
		return fmt.Errorf("audience %q not present", a.config.Audience)
	}

	now := time.Now()
	if claims.ExpiresAt == 0 {
		return errors.New("exp claim is required")
	}
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(defaultJWTClockSkew)) {
		return errors.New("token is expired")
	}
	if claims.NotBefore > 0 && now.Add(defaultJWTClockSkew).Before(time.Unix(claims.NotBefore, 0)) {
		return errors.New("token is not yet valid")
	}
	if claims.Subject == "" {
		return errors.New("sub claim is required")
	}

	return nil
}

func (a audienceClaim) Contains(expected string) bool {
	for _, audience := range a {
		if audience == expected {
			return true
		}
	}
	return false
}

func hashJWTSigningInput(alg, signingInput string) (crypto.Hash, []byte, error) {
	switch alg {
	case "RS256":
		sum := sha256.Sum256([]byte(signingInput))
		return crypto.SHA256, sum[:], nil
	case "RS384":
		sum := sha512.Sum384([]byte(signingInput))
		return crypto.SHA384, sum[:], nil
	case "RS512":
		sum := sha512.Sum512([]byte(signingInput))
		return crypto.SHA512, sum[:], nil
	default:
		return 0, nil, fmt.Errorf("unsupported JWT signing algorithm %q", alg)
	}
}

func (a *BearerTokenAuthenticator) getKey(kid string) *rsa.PublicKey {
	a.keysLock.RLock()
	defer a.keysLock.RUnlock()
	return a.keys[kid]
}

func (a *BearerTokenAuthenticator) refreshKeys(ctx context.Context) error {
	jwksURL, err := a.resolveJWKSURL(ctx)
	if err != nil {
		return err
	}
	if urlErr := validateOAuthHTTPURL(jwksURL); urlErr != nil {
		return fmt.Errorf("invalid JWKS URL: %w", urlErr)
	}

	// #nosec G704 -- URL comes from validated OAuth issuer/JWKS configuration.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, http.NoBody)
	if err != nil {
		return err
	}
	// #nosec G704 -- URL comes from validated OAuth issuer/JWKS configuration.
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthHTTPResponseBytes))
	if err != nil {
		return fmt.Errorf("read JWKS: %w", err)
	}

	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)
	for _, key := range doc.Keys {
		if key.Kid == "" || key.Kty != "RSA" {
			continue
		}
		publicKey, err := key.RSAPublicKey()
		if err != nil {
			a.log.V(1).Info("Skipping invalid JWKS RSA key", "kid", key.Kid, "error", err)
			continue
		}
		keys[key.Kid] = publicKey
	}
	if len(keys) == 0 {
		return errors.New("JWKS did not contain usable RSA keys")
	}

	a.keysLock.Lock()
	a.keys = keys
	a.jwksURL = jwksURL
	a.keysLock.Unlock()
	return nil
}

func (a *BearerTokenAuthenticator) resolveJWKSURL(ctx context.Context) (string, error) {
	if a.config.JWKSUrl != "" {
		return a.config.JWKSUrl, nil
	}

	a.keysLock.RLock()
	cachedURL := a.jwksURL
	a.keysLock.RUnlock()
	if cachedURL != "" {
		return cachedURL, nil
	}

	issuer := strings.TrimRight(a.config.Issuer, "/")
	discoveryURL := issuer + "/.well-known/openid-configuration"
	if err := validateOAuthHTTPURL(discoveryURL); err != nil {
		return "", fmt.Errorf("invalid OIDC discovery URL: %w", err)
	}
	// #nosec G704 -- URL comes from validated OAuth issuer configuration.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, http.NoBody)
	if err != nil {
		return "", err
	}
	// #nosec G704 -- URL comes from validated OAuth issuer configuration.
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch OIDC discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OIDC discovery returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthHTTPResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read OIDC discovery: %w", err)
	}

	var doc oidcDiscoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("parse OIDC discovery: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("OIDC discovery did not include jwks_uri")
	}
	if err := validateOAuthHTTPURL(doc.JWKSURI); err != nil {
		return "", fmt.Errorf("invalid discovered JWKS URL: %w", err)
	}

	return doc.JWKSURI, nil
}

func validateOAuthHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("host is required")
	}
	if parsed.Scheme == "http" && !isLoopbackOAuthHost(parsed.Hostname()) {
		return errors.New("http is allowed only for loopback OAuth endpoints")
	}
	if parsed.User != nil {
		return errors.New("userinfo is not allowed")
	}
	return nil
}

func isLoopbackOAuthHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (k *jwkKey) RSAPublicKey() (*rsa.PublicKey, error) {
	modulusBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	exponent := big.NewInt(0).SetBytes(exponentBytes).Int64()
	if exponent <= 0 || exponent > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("invalid exponent %d", exponent)
	}

	return &rsa.PublicKey{
		N: big.NewInt(0).SetBytes(modulusBytes),
		E: int(exponent),
	}, nil
}
