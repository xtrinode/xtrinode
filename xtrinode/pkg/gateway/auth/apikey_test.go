package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newTestAuthenticator creates an API key authenticator with test configuration
func newTestAuthenticator(cli client.Client, secretData string) (*APIKeyAuthenticator, error) {
	log := logr.Discard()
	config := &APIKeyConfig{
		SecretName: "test-secret",
		SecretKey:  "api-keys",
		Namespace:  "test-namespace",
	}

	auth, err := NewAPIKeyAuthenticator(cli, log, config)
	if err != nil {
		return nil, err
	}

	// Create test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-namespace",
		},
		Data: map[string][]byte{
			"api-keys": []byte(secretData),
		},
		Type: corev1.SecretTypeOpaque,
	}

	if err := cli.Create(context.Background(), secret); err != nil {
		return nil, err
	}

	return auth, nil
}

// newTestRequest creates an HTTP request with optional API key header
func newTestRequest(method, path, apiKey string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	if apiKey != "" {
		req.Header.Set(APIKeyHeader, apiKey)
	}
	return req
}

func TestParseAPIKeys(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		expected map[string]string
	}{
		{
			name: "single key",
			data: "service-1: key-abc123xyz789",
			expected: map[string]string{
				"key-abc123xyz789": "service-1",
			},
		},
		{
			name: "multiple keys",
			data: `service-1: key-abc123xyz789
service-2: key-def456uvw012`,
			expected: map[string]string{
				"key-abc123xyz789": "service-1",
				"key-def456uvw012": "service-2",
			},
		},
		{
			name: "multiple keys per keyID (rotation)",
			data: "service-1: key-old-abc123456,key-new-xyz789012",
			expected: map[string]string{
				"key-old-abc123456": "service-1",
				"key-new-xyz789012": "service-1",
			},
		},
		{
			name: "keys with quotes",
			data: `service-1: "key-abc123xyz789"
service-2: 'key-def456uvw012'`,
			expected: map[string]string{
				"key-abc123xyz789": "service-1",
				"key-def456uvw012": "service-2",
			},
		},
		{
			name:     "empty data",
			data:     "",
			expected: map[string]string{},
		},
		{
			name: "with comments",
			data: `# This is a comment
service-1: key-abc123xyz789
# Another comment
service-2: key-def456uvw012`,
			expected: map[string]string{
				"key-abc123xyz789": "service-1",
				"key-def456uvw012": "service-2",
			},
		},
		{
			name: "with empty lines",
			data: `service-1: key-abc123xyz789

service-2: key-def456uvw012`,
			expected: map[string]string{
				"key-abc123xyz789": "service-1",
				"key-def456uvw012": "service-2",
			},
		},
		{
			name:     "key too short (should be filtered)",
			data:     "service-1: short",
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseAPIKeys(tt.data)
			if err != nil {
				t.Fatalf("parseAPIKeys() error = %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Errorf("parseAPIKeys() length = %d, expected %d", len(result), len(tt.expected))
			}

			for key, keyID := range tt.expected {
				if result[key] != keyID {
					t.Errorf("parseAPIKeys() key %s = %s, expected %s", key, result[key], keyID)
				}
			}
		})
	}
}

func TestAPIKeyAuthenticator_Authenticate(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretData := `service-1: key-abc123xyz789
service-2: key-def456uvw012
service-3: key-old-abc123456,key-new-xyz789012`

	auth, err := newTestAuthenticator(cli, secretData)
	if err != nil {
		t.Fatalf("newTestAuthenticator() error = %v", err)
	}

	// Load initial keys
	ctx := context.Background()
	if err := auth.reloadSecret(ctx); err != nil {
		t.Fatalf("reloadSecret() error = %v", err)
	}

	tests := []struct {
		name          string
		apiKey        string
		expectedAuth  bool
		expectedKeyID string
	}{
		{
			name:          "valid key",
			apiKey:        "key-abc123xyz789",
			expectedAuth:  true,
			expectedKeyID: "service-1",
		},
		{
			name:          "valid key for service-2",
			apiKey:        "key-def456uvw012",
			expectedAuth:  true,
			expectedKeyID: "service-2",
		},
		{
			name:          "invalid key",
			apiKey:        "key-invalid",
			expectedAuth:  false,
			expectedKeyID: "",
		},
		{
			name:          "missing header",
			apiKey:        "",
			expectedAuth:  false,
			expectedKeyID: "",
		},
		{
			name:          "old key during rotation",
			apiKey:        "key-old-abc123456",
			expectedAuth:  true,
			expectedKeyID: "service-3",
		},
		{
			name:          "new key during rotation",
			apiKey:        "key-new-xyz789012",
			expectedAuth:  true,
			expectedKeyID: "service-3",
		},
		{
			name:          "key too short",
			apiKey:        "short",
			expectedAuth:  false,
			expectedKeyID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newTestRequest("POST", "/v1/statement", tt.apiKey)
			result, err := auth.Authenticate(req)

			if err != nil {
				t.Fatalf("Authenticate() error = %v", err)
			}

			if result == nil {
				if tt.expectedAuth {
					t.Errorf("Authenticate() result = nil, expected authenticated")
				}
				return
			}

			if result.Authenticated != tt.expectedAuth {
				t.Errorf("Authenticate() authenticated = %v, expected %v", result.Authenticated, tt.expectedAuth)
			}

			if result.KeyID != tt.expectedKeyID {
				t.Errorf("Authenticate() keyID = %s, expected %s", result.KeyID, tt.expectedKeyID)
			}
		})
	}
}

func TestAPIKeyAuthenticator_Reload(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretData := `service-1: key-abc123xyz789`

	auth, err := newTestAuthenticator(cli, secretData)
	if err != nil {
		t.Fatalf("newTestAuthenticator() error = %v", err)
	}

	ctx := context.Background()

	// Load initial keys
	if reloadErr := auth.reloadSecret(ctx); reloadErr != nil {
		t.Fatalf("reloadSecret() error = %v", reloadErr)
	}

	// Verify initial key works
	req := newTestRequest("POST", "/v1/statement", "key-abc123xyz789")
	result, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !result.Authenticated || result.KeyID != "service-1" {
		t.Errorf("Initial key should work")
	}

	// Update secret - get current, modify, and update
	secret := &corev1.Secret{}
	if getErr := cli.Get(ctx, client.ObjectKey{Name: "test-secret", Namespace: "test-namespace"}, secret); getErr != nil {
		t.Fatalf("Get secret error = %v", getErr)
	}

	// Update the secret data and resourceVersion to force reload
	secret.Data["api-keys"] = []byte(`service-1: key-new-xyz789012
service-2: key-def456uvw012`)
	secret.ResourceVersion = "2" // Force new resource version

	if updateErr := cli.Update(ctx, secret); updateErr != nil {
		// If Update fails due to resourceVersion conflict, try delete+create
		if delErr := cli.Delete(ctx, secret); delErr == nil {
			updatedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"api-keys": []byte(`service-1: key-new-xyz789012
service-2: key-def456uvw012`),
				},
				Type: corev1.SecretTypeOpaque,
			}
			if createErr := cli.Create(ctx, updatedSecret); createErr != nil {
				t.Fatalf("Create updated secret error = %v", createErr)
			}
		} else {
			t.Fatalf("Update secret error = %v", updateErr)
		}
	}

	// Force reload by clearing lastResourceVersion to ensure reload happens
	// (fake client might not properly track resourceVersion changes)
	auth.keysLock.Lock()
	auth.lastResourceVersion = ""
	auth.keysLock.Unlock()

	// Reload keys
	if reloadErr2 := auth.reloadSecret(ctx); reloadErr2 != nil {
		t.Fatalf("reloadSecret() error = %v", reloadErr2)
	}

	// Old key should not work
	req = newTestRequest("POST", "/v1/statement", "key-abc123xyz789")
	result, err = auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.Authenticated {
		t.Errorf("Old key should not work after reload")
	}

	// New keys should work
	req = newTestRequest("POST", "/v1/statement", "key-new-xyz789012")
	result, err = auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !result.Authenticated || result.KeyID != "service-1" {
		t.Errorf("New key should work")
	}

	req = newTestRequest("POST", "/v1/statement", "key-def456uvw012")
	result, err = auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !result.Authenticated || result.KeyID != "service-2" {
		t.Errorf("Service 2 key should work")
	}
}

func TestAPIKeyAuthenticator_Concurrent(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretData := `service-1: key-abc123xyz789
service-2: key-def456uvw012`

	auth, err := newTestAuthenticator(cli, secretData)
	if err != nil {
		t.Fatalf("newTestAuthenticator() error = %v", err)
	}

	ctx := context.Background()
	if err := auth.reloadSecret(ctx); err != nil {
		t.Fatalf("reloadSecret() error = %v", err)
	}

	// Test concurrent authentication
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			req := newTestRequest("POST", "/v1/statement", "key-abc123xyz789")
			result, err := auth.Authenticate(req)
			if err != nil {
				t.Errorf("Authenticate() error = %v", err)
			}
			if !result.Authenticated {
				t.Errorf("Authenticate() should succeed")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestAPIKeyAuthenticator_NewAPIKeyAuthenticator(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	tests := []struct {
		name    string
		config  *APIKeyConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &APIKeyConfig{
				SecretName: "test-secret",
				SecretKey:  "api-keys",
				Namespace:  "test-namespace",
			},
			wantErr: false,
		},
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "missing secret name",
			config: &APIKeyConfig{
				SecretKey: "api-keys",
				Namespace: "test-namespace",
			},
			wantErr: true,
		},
		{
			name: "missing secret key",
			config: &APIKeyConfig{
				SecretName: "test-secret",
				Namespace:  "test-namespace",
			},
			wantErr: true,
		},
		{
			name: "missing namespace",
			config: &APIKeyConfig{
				SecretName: "test-secret",
				SecretKey:  "api-keys",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAPIKeyAuthenticator(cli, log, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewAPIKeyAuthenticator() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAPIKeyAuthenticator_Start(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretData := `service-1: key-abc123xyz789`

	auth, err := newTestAuthenticator(cli, secretData)
	if err != nil {
		t.Fatalf("newTestAuthenticator() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start authenticator (should load keys and start watching)
	if startErr := auth.Start(ctx); startErr != nil {
		t.Fatalf("Start() error = %v", startErr)
	}

	// Give it a moment to load
	time.Sleep(100 * time.Millisecond)

	// Verify keys are loaded
	req := newTestRequest("POST", "/v1/statement", "key-abc123xyz789")
	result, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !result.Authenticated {
		t.Errorf("Key should be loaded after Start()")
	}
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{
			name:     "long key",
			key:      "key-abc123xyz789",
			expected: "key-...z789",
		},
		{
			name:     "short key",
			key:      "short",
			expected: "****",
		},
		{
			name:     "exactly 8 chars",
			key:      "12345678",
			expected: "****",
		},
		{
			name:     "9 chars",
			key:      "123456789",
			expected: "1234...6789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskKey(tt.key)
			if result != tt.expected {
				t.Errorf("maskKey() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		authenticator  Authenticator
		apiKey         string
		expectedStatus int
		expectAuth     bool
	}{
		{
			name:           "nil authenticator (disabled)",
			authenticator:  nil,
			apiKey:         "",
			expectedStatus: http.StatusOK,
			expectAuth:     false,
		},
		{
			name:           "valid API key",
			authenticator:  createMockAuthenticator(true, "service-1"),
			apiKey:         "valid-key",
			expectedStatus: http.StatusOK,
			expectAuth:     true,
		},
		{
			name:           "invalid API key",
			authenticator:  createMockAuthenticator(false, ""),
			apiKey:         "invalid-key",
			expectedStatus: http.StatusUnauthorized,
			expectAuth:     false,
		},
		{
			name:           "missing API key",
			authenticator:  createMockAuthenticator(false, ""),
			apiKey:         "",
			expectedStatus: http.StatusUnauthorized,
			expectAuth:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test handler
			handlerCalled := false
			var authResult *Result
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				authResult = GetAuthResult(r)
				w.WriteHeader(http.StatusOK)
			})

			// Apply middleware
			middleware := Middleware(tt.authenticator)
			wrappedHandler := middleware(nextHandler)

			// Create request
			req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
			if tt.apiKey != "" {
				req.Header.Set(APIKeyHeader, tt.apiKey)
			}

			// Execute request
			recorder := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(recorder, req)

			// Verify status code
			if recorder.Code != tt.expectedStatus {
				t.Errorf("Middleware() status = %d, expected %d", recorder.Code, tt.expectedStatus)
			}

			// Verify handler was called (only if auth passed or disabled)
			if tt.expectedStatus == http.StatusOK && !handlerCalled {
				t.Errorf("Middleware() handler was not called")
			}

			// Verify auth result in context
			if tt.expectAuth {
				if authResult == nil || !authResult.Authenticated {
					t.Errorf("Middleware() auth result = %v, expected authenticated", authResult)
				}
			}
		})
	}
}

func TestMiddleware_AuthenticationError(t *testing.T) {
	// Create authenticator that returns error
	errorAuth := &mockAuthenticator{
		authenticateFunc: func(r *http.Request) (*Result, error) {
			return nil, fmt.Errorf("secret lookup failed")
		},
	}

	handlerCalled := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	})

	middleware := Middleware(errorAuth)
	wrappedHandler := middleware(nextHandler)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/statement", http.NoBody)
	recorder := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(recorder, req)

	// Should return 500 Internal Server Error
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("Middleware() status = %d, expected %d", recorder.Code, http.StatusInternalServerError)
	}

	// Handler should not be called
	if handlerCalled {
		t.Errorf("Middleware() handler should not be called on authentication error")
	}
}

func TestGetAuthResult(t *testing.T) {
	tests := []struct {
		name           string
		setupContext   func(*http.Request) *http.Request
		expectedResult *Result
	}{
		{
			name: "auth result present",
			setupContext: func(r *http.Request) *http.Request {
				result := &Result{
					Authenticated: true,
					KeyID:         "service-1",
					User:          "service-1",
				}
				ctx := withAuthResult(r.Context(), result)
				return r.WithContext(ctx)
			},
			expectedResult: &Result{
				Authenticated: true,
				KeyID:         "service-1",
				User:          "service-1",
			},
		},
		{
			name: "no auth result",
			setupContext: func(r *http.Request) *http.Request {
				return r
			},
			expectedResult: nil,
		},
		{
			name: "wrong context value type",
			setupContext: func(r *http.Request) *http.Request {
				ctx := context.WithValue(r.Context(), authResultKey, "not-a-result")
				return r.WithContext(ctx)
			},
			expectedResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
			req = tt.setupContext(req)

			result := GetAuthResult(req)

			if tt.expectedResult == nil {
				if result != nil {
					t.Errorf("GetAuthResult() = %v, expected nil", result)
				}
			} else {
				if result == nil {
					t.Errorf("GetAuthResult() = nil, expected %v", tt.expectedResult)
				} else if result.KeyID != tt.expectedResult.KeyID || result.Authenticated != tt.expectedResult.Authenticated {
					t.Errorf("GetAuthResult() = %v, expected %v", result, tt.expectedResult)
				}
			}
		})
	}
}

func TestAPIKeyAuthenticator_Start_Error(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	config := &APIKeyConfig{
		SecretName: "non-existent-secret",
		SecretKey:  "api-keys",
		Namespace:  "test-namespace",
	}

	auth, err := NewAPIKeyAuthenticator(cli, log, config)
	if err != nil {
		t.Fatalf("NewAPIKeyAuthenticator() error = %v", err)
	}

	ctx := context.Background()

	// Start should fail because secret doesn't exist
	err = auth.Start(ctx)
	if err == nil {
		t.Errorf("Start() error = nil, expected error for missing secret")
	}
}

func TestAPIKeyAuthenticator_Start_ContextCancellation(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretData := `service-1: key-abc123xyz789`
	auth, err := newTestAuthenticator(cli, secretData)
	if err != nil {
		t.Fatalf("newTestAuthenticator() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start authenticator
	if startErr := auth.Start(ctx); startErr != nil {
		t.Fatalf("Start() error = %v", startErr)
	}

	// Give it a moment to start watching
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Give watcher time to stop
	time.Sleep(100 * time.Millisecond)

	// Verify authentication still works (keys should be loaded)
	req := newTestRequest("POST", "/v1/statement", "key-abc123xyz789")
	result, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !result.Authenticated {
		t.Errorf("Authenticate() should still work after context cancellation")
	}
}

// mockAuthenticator is a test implementation of Authenticator
type mockAuthenticator struct {
	authenticateFunc func(*http.Request) (*Result, error)
}

func (m *mockAuthenticator) Authenticate(r *http.Request) (*Result, error) {
	if m.authenticateFunc != nil {
		return m.authenticateFunc(r)
	}
	return &Result{Authenticated: false}, nil
}

// createMockAuthenticator creates a mock authenticator for testing
func createMockAuthenticator(authenticated bool, keyID string) Authenticator {
	return &mockAuthenticator{
		authenticateFunc: func(r *http.Request) (*Result, error) {
			apiKey := r.Header.Get(APIKeyHeader)
			if apiKey == "valid-key" && authenticated {
				return &Result{
					Authenticated: true,
					KeyID:         keyID,
					User:          keyID,
					Metadata:      map[string]string{"auth_type": "api-key"},
				}, nil
			}
			return &Result{Authenticated: false}, nil
		},
	}
}
