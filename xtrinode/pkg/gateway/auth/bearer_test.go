package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func TestBearerTokenAuthenticator_Authenticate(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	issuer := "https://issuer.example.test"
	audience := "xtrinode-gateway"
	kid := "test-key"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/jwks", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{rsaPublicJWK(kid, &privateKey.PublicKey)},
		})
	}))
	defer jwksServer.Close()

	authenticator, err := NewBearerTokenAuthenticator(logr.Discard(), &BearerTokenConfig{
		Issuer:   issuer,
		Audience: audience,
		JWKSUrl:  jwksServer.URL + "/jwks",
	})
	require.NoError(t, err)
	require.NoError(t, authenticator.Start(context.Background()))

	token := signedTestJWT(t, privateKey, kid, map[string]any{
		"iss":                issuer,
		"aud":                audience,
		"sub":                "user-123",
		"preferred_username": "alice",
		"exp":                time.Now().Add(time.Hour).Unix(),
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)

	result, err := authenticator.Authenticate(req)
	require.NoError(t, err)
	require.True(t, result.Authenticated)
	require.Equal(t, "user-123", result.KeyID)
	require.Equal(t, "alice", result.User)
}

func TestBearerTokenAuthenticator_RejectsWrongAudience(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	kid := "test-key"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{rsaPublicJWK(kid, &privateKey.PublicKey)},
		})
	}))
	defer jwksServer.Close()

	authenticator, err := NewBearerTokenAuthenticator(logr.Discard(), &BearerTokenConfig{
		Issuer:   "https://issuer.example.test",
		Audience: "expected-audience",
		JWKSUrl:  jwksServer.URL,
	})
	require.NoError(t, err)
	require.NoError(t, authenticator.Start(context.Background()))

	token := signedTestJWT(t, privateKey, kid, map[string]any{
		"iss": "https://issuer.example.test",
		"aud": "wrong-audience",
		"sub": "user-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)

	result, err := authenticator.Authenticate(req)
	require.NoError(t, err)
	require.False(t, result.Authenticated)
}

func TestBearerTokenAuthenticator_RequiresAudience(t *testing.T) {
	_, err := NewBearerTokenAuthenticator(logr.Discard(), &BearerTokenConfig{
		Issuer: "https://issuer.example.test",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "auth-oauth-audience")
}

func TestBearerTokenAuthenticator_RejectsNonLoopbackHTTPURL(t *testing.T) {
	_, err := NewBearerTokenAuthenticator(logr.Discard(), &BearerTokenConfig{
		Issuer:   "http://issuer.example.test",
		Audience: "xtrinode-gateway",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "loopback")
}

func signedTestJWT(t *testing.T, privateKey *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()

	header := map[string]any{
		"alg": "RS256",
		"kid": kid,
		"typ": "JWT",
	}
	headerJSON, err := json.Marshal(header)
	require.NoError(t, err)
	claimsJSON, err := json.Marshal(claims)
	require.NoError(t, err)

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	require.NoError(t, err)

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func rsaPublicJWK(kid string, publicKey *rsa.PublicKey) map[string]string {
	return map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(publicKey.E)).Bytes()),
	}
}

func TestMiddleware_BearerChallenge(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer invalid")
	rec := httptest.NewRecorder()

	Middleware(mockBearerAuthenticator{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
}

type mockBearerAuthenticator struct{}

func (mockBearerAuthenticator) Authenticate(*http.Request) (*Result, error) {
	return &Result{Authenticated: false}, nil
}

func TestAudienceClaimUnmarshal(t *testing.T) {
	var single audienceClaim
	require.NoError(t, json.Unmarshal([]byte(`"one"`), &single))
	require.True(t, single.Contains("one"))

	var multiple audienceClaim
	require.NoError(t, json.Unmarshal([]byte(`["one","two"]`), &multiple))
	require.True(t, multiple.Contains("two"))
	require.False(t, multiple.Contains("three"))
}

func TestHashJWTSigningInputRejectsUnsupportedAlg(t *testing.T) {
	_, _, err := hashJWTSigningInput("none", "a.b")
	require.Error(t, err)
	require.True(t, strings.Contains(fmt.Sprint(err), "unsupported"))
}
