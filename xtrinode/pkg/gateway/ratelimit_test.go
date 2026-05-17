package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/redis/go-redis/v9"
)

func newTestRateLimiter(capacity int, refillRate time.Duration) *RateLimiter {
	return NewRateLimiter(capacity, refillRate, logr.Discard())
}

func TestRateLimiter_NewRateLimiter(t *testing.T) {
	rl := newTestRateLimiter(100, 1*time.Second)
	if rl == nil {
		t.Fatal("Expected rate limiter, got nil")
	}
	if rl.capacity != 100 {
		t.Errorf("Expected capacity 100, got %d", rl.capacity)
	}
	if rl.refillRate != 1*time.Second {
		t.Errorf("Expected refill rate 1s, got %v", rl.refillRate)
	}
}

func TestTokenBucket_NewTokenBucket(t *testing.T) {
	tb := NewTokenBucket(10, 1*time.Second)
	if tb == nil {
		t.Fatal("Expected token bucket, got nil")
	}
	if tb.Capacity != 10 {
		t.Errorf("Expected capacity 10, got %d", tb.Capacity)
	}
	if tb.Tokens != 10 {
		t.Errorf("Expected initial tokens 10, got %d", tb.Tokens)
	}
}

func TestTokenBucket_Allow_WithTokens(t *testing.T) {
	tb := NewTokenBucket(10, 1*time.Second)

	// Should allow when tokens available
	for i := 0; i < 10; i++ {
		if !tb.Allow() {
			t.Errorf("Expected Allow() to return true (iteration %d)", i)
		}
	}

	// Should be empty now
	if tb.Allow() {
		t.Error("Expected Allow() to return false (no tokens)")
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	tb := NewTokenBucket(10, 100*time.Millisecond)

	// Consume all tokens
	for i := 0; i < 10; i++ {
		tb.Allow()
	}

	if tb.Allow() {
		t.Error("Expected no tokens available")
	}

	// Wait for refill
	time.Sleep(150 * time.Millisecond)

	// Should have at least 1 token
	if !tb.Allow() {
		t.Error("Expected token after refill")
	}
}

func TestTokenBucket_Refill_Cap(t *testing.T) {
	tb := NewTokenBucket(10, 100*time.Millisecond)

	// Consume all tokens
	for i := 0; i < 10; i++ {
		tb.Allow()
	}

	// Wait for more than capacity refills
	time.Sleep(2 * time.Second)

	// Should be capped at capacity
	tb.mu.Lock()
	if tb.Tokens > tb.Capacity {
		t.Errorf("Expected tokens <= capacity (%d), got %d", tb.Capacity, tb.Tokens)
	}
	tb.mu.Unlock()
}

func TestRateLimiter_Allow_NewKey(t *testing.T) {
	rl := newTestRateLimiter(10, 1*time.Second)

	// New key should be allowed
	if !rl.Allow("key1") {
		t.Error("Expected Allow() to return true for new key")
	}
}

func TestRateLimiter_Allow_MultipleKeys(t *testing.T) {
	rl := newTestRateLimiter(10, 1*time.Second)

	// Different keys should have separate buckets
	for i := 0; i < 10; i++ {
		if !rl.Allow("key1") {
			t.Errorf("Expected Allow() to return true for key1 (iteration %d)", i)
		}
	}

	// key2 should still have tokens
	if !rl.Allow("key2") {
		t.Error("Expected Allow() to return true for key2 (separate bucket)")
	}
}

func TestRateLimiter_Allow_RateLimit(t *testing.T) {
	rl := newTestRateLimiter(5, 1*time.Second)

	// Consume all tokens
	for i := 0; i < 5; i++ {
		if !rl.Allow("key1") {
			t.Errorf("Expected Allow() to return true (iteration %d)", i)
		}
	}

	// Should be rate limited
	if rl.Allow("key1") {
		t.Error("Expected Allow() to return false (rate limited)")
	}
}

func TestRateLimiter_CleanupRemovesOnlyInactiveBuckets(t *testing.T) {
	rl := newTestRateLimiter(10, time.Second)
	rl.cleanupTTL = time.Minute

	oldBucket := NewTokenBucket(10, time.Second)
	oldBucket.LastSeen = time.Now().Add(-2 * time.Minute)
	recentBucket := NewTokenBucket(10, time.Second)
	recentBucket.LastSeen = time.Now()

	rl.limiters["old"] = oldBucket
	rl.limiters["recent"] = recentBucket

	rl.cleanup()

	if _, exists := rl.limiters["old"]; exists {
		t.Fatal("expected inactive bucket to be removed")
	}
	if _, exists := rl.limiters["recent"]; !exists {
		t.Fatal("expected recent bucket to be kept")
	}
}

func TestRateLimiter_StartCleanupReturnsWhenContextCanceled(t *testing.T) {
	rl := newTestRateLimiter(10, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		rl.StartCleanup(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartCleanup did not return after context cancellation")
	}
}

func TestRateLimiter_AllowRedisReturnsErrorWhenRedisUnavailable(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
	})
	defer redisClient.Close()

	rl := NewDistributedRateLimiter(1, time.Second, redisClient, 20*time.Millisecond, logr.Discard())
	allowed, err := rl.allowRedis("key")
	if err == nil {
		t.Fatal("expected Redis command error")
	}
	if allowed {
		t.Fatal("expected Redis error result to be denied")
	}
}

func TestRateLimiter_AllowRedisUsesTokenBucket(t *testing.T) {
	server := startRedisRESPTestServer(t)
	redisClient := redis.NewClient(&redis.Options{
		Addr:            server.addr(),
		Protocol:        2,
		DisableIdentity: true,
	})
	defer redisClient.Close()

	rl := NewDistributedRateLimiter(1, 40*time.Millisecond, redisClient, time.Second, logr.Discard())
	allowed, err := rl.allowRedis("key")
	if err != nil {
		t.Fatalf("expected Redis allow to succeed: %v", err)
	}
	if !allowed {
		t.Fatal("expected first distributed request to be allowed")
	}

	allowed, err = rl.allowRedis("key")
	if err != nil {
		t.Fatalf("expected Redis allow to succeed: %v", err)
	}
	if allowed {
		t.Fatal("expected second request without refill to be denied")
	}

	time.Sleep(60 * time.Millisecond)

	allowed, err = rl.allowRedis("key")
	if err != nil {
		t.Fatalf("expected Redis allow to succeed after refill: %v", err)
	}
	if !allowed {
		t.Fatal("expected token bucket to allow after refill interval")
	}
}

func TestRateLimiter_AllowFallsBackWhenRedisCommandFails(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
	})
	defer redisClient.Close()

	rl := NewDistributedRateLimiter(1, time.Second, redisClient, 20*time.Millisecond, logr.Discard())
	if !rl.Allow("key") {
		t.Fatal("expected first request to fall back to local bucket")
	}
	if rl.Allow("key") {
		t.Fatal("expected local fallback bucket to enforce capacity")
	}
}

func TestExtractRateLimitKey_IgnoresRawAPIKeyHeader(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("X-API-Key", "test-api-key")

	key := extractRateLimitKey(req)
	if key != "ip:192.168.1.1" {
		t.Errorf("Expected IP key despite API key header, got %s", key)
	}
}

func TestExtractRateLimitKey_IgnoresRawAuthorizationHeader(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("Authorization", "Bearer test-token-123")

	key := extractRateLimitKey(req)
	if key != "ip:192.168.1.1" {
		t.Errorf("Expected IP key despite Authorization header, got %s", key)
	}
}

func TestExtractRateLimitKey_IPAddress(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		expected   string
	}{
		{
			name:       "IPv4 with port",
			remoteAddr: "192.168.1.1:12345",
			// Port should be stripped for rate limiting by IP address
			expected: "ip:192.168.1.1",
		},
		{
			name:       "IPv4 without port",
			remoteAddr: "192.168.1.1",
			expected:   "ip:192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr

			key := extractRateLimitKey(req)
			if key != tt.expected {
				t.Errorf("Expected key %s, got %s", tt.expected, key)
			}
		})
	}
}

func TestExtractRateLimitKey_Priority(t *testing.T) {
	// Priority order: RemoteAddr > X-Forwarded-For only without RemoteAddr.

	// Test 1: raw auth headers do not override client IP because rate limiting
	// runs before authentication.
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req.Header.Set("X-API-Key", "api-key")
	req.Header.Set("Authorization", "Bearer token")
	req.RemoteAddr = "192.168.1.1:12345"

	key := extractRateLimitKey(req)
	if key != "ip:192.168.1.1" {
		t.Errorf("Expected RemoteAddr priority over auth headers, got %s", key)
	}

	// Test 2: Authorization is also ignored when no API key is present.
	req.Header.Del("X-API-Key")
	key = extractRateLimitKey(req)
	if key != "ip:192.168.1.1" {
		t.Errorf("Expected RemoteAddr priority over Authorization header, got %s", key)
	}

	// Test 3: X-Forwarded-For does not override RemoteAddr.
	req.Header.Del("Authorization")
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 192.168.1.1")
	key = extractRateLimitKey(req)
	if key != "ip:192.168.1.1" {
		t.Errorf("Expected RemoteAddr priority over X-Forwarded-For, got %s", key)
	}

	// Test 4: X-Forwarded-For is only used when RemoteAddr is unavailable.
	req.RemoteAddr = ""
	key = extractRateLimitKey(req)
	if key != "ip:10.0.0.1" {
		t.Errorf("Expected X-Forwarded-For fallback, got %s", key)
	}

	// Test 5: Unknown when no identity or IP is available.
	req.Header.Del("X-Forwarded-For")
	key = extractRateLimitKey(req)
	if key != "unknown" {
		t.Errorf("Expected unknown fallback, got %s", key)
	}
}

func TestRateLimitMiddleware_Allow(t *testing.T) {
	rl := newTestRateLimiter(10, 1*time.Second)
	middleware := RateLimitMiddleware(rl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(handler)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")

	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestRateLimitMiddleware_RateLimited(t *testing.T) {
	rl := newTestRateLimiter(2, 1*time.Second)
	middleware := RateLimitMiddleware(rl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(handler)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")

	// Consume all tokens
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200 (iteration %d), got %d", i, w.Code)
		}
	}

	// Should be rate limited
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", w.Code)
	}
	if w.Header().Get("X-RateLimit-Limit") != "exceeded" {
		t.Error("Expected X-RateLimit-Limit header")
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("Expected Retry-After header")
	}
}

func TestRateLimitMiddleware_DifferentKeys(t *testing.T) {
	rl := newTestRateLimiter(2, 1*time.Second)
	middleware := RateLimitMiddleware(rl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(handler)

	// Rate limit key1
	req1 := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req1.RemoteAddr = "192.168.1.1:12345"
	req1.Header.Set("X-API-Key", "key1")

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req1)
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200 for key1 (iteration %d), got %d", i, w.Code)
		}
	}

	// key2 should still work
	req2 := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req2.RemoteAddr = "192.168.1.2:12345"
	req2.Header.Set("X-API-Key", "key2")

	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req2)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for key2, got %d", w.Code)
	}

	// key1 should be rate limited
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, req1)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429 for key1, got %d", w.Code)
	}
}

func TestRateLimitMiddleware_DifferentFakeCredentialsDoNotBypassIPLimit(t *testing.T) {
	rl := newTestRateLimiter(1, time.Hour)
	middleware := RateLimitMiddleware(rl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(handler)

	req1 := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req1.RemoteAddr = "192.168.1.1:12345"
	req1.Header.Set("X-API-Key", "fake-key-1")

	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req1)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected first request to pass, got %d", w.Code)
	}

	req2 := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req2.RemoteAddr = "192.168.1.1:12345"
	req2.Header.Set("X-API-Key", "fake-key-2")
	req2.Header.Set("Authorization", "Bearer fake-token-2")

	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, req2)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("Expected rotated fake credentials to stay in the same IP bucket, got %d", w.Code)
	}
}

func TestRateLimitMiddleware_DifferentFakeCredentialsDoNotBypassForwardedForLimit(t *testing.T) {
	rl := newTestRateLimiter(1, time.Hour)
	middleware := RateLimitMiddleware(rl)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(handler)

	req1 := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req1.RemoteAddr = ""
	req1.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.10")
	req1.Header.Set("X-API-Key", "fake-key-1")

	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req1)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected first forwarded request to pass, got %d", w.Code)
	}

	req2 := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	req2.RemoteAddr = ""
	req2.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.10")
	req2.Header.Set("X-API-Key", "fake-key-2")
	req2.Header.Set("Authorization", "Bearer fake-token-2")

	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, req2)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("Expected rotated fake credentials to stay in the same forwarded IP bucket, got %d", w.Code)
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := newTestRateLimiter(100, 1*time.Second)

	key := "test-key"
	iterations := 50
	allowed := 0

	var mu sync.Mutex

	// Concurrent Allow() calls
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if rl.Allow(key) {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	// Should have allowed at least some requests
	if allowed == 0 {
		t.Error("Expected at least some requests to be allowed")
	}

	// Should not exceed capacity significantly (allowing for race conditions)
	if allowed > 150 {
		t.Errorf("Expected allowed <= 150 (allowing race conditions), got %d", allowed)
	}
}

func TestTokenBucket_ConcurrentAccess(t *testing.T) {
	tb := NewTokenBucket(100, 1*time.Second)

	allowed := 0
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if tb.Allow() {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	// Should have allowed exactly capacity (allowing for race conditions)
	if allowed < 90 || allowed > 110 {
		t.Errorf("Expected allowed ~100 (allowing race conditions), got %d", allowed)
	}
}
