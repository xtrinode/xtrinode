package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/redis/go-redis/v9"
)

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	Capacity   int           // Maximum number of tokens
	Tokens     int           // Current number of tokens
	RefillRate time.Duration // How often to add one token
	LastRefill time.Time
	LastSeen   time.Time // Last time Allow() was called (for cleanup)
	mu         sync.Mutex
}

// NewTokenBucket creates a new token bucket
func NewTokenBucket(capacity int, refillRate time.Duration) *TokenBucket {
	// Guard against zero/negative refillRate to prevent division-by-zero panic
	if refillRate <= 0 {
		refillRate = time.Second // Safe default: 1 token per second
	}
	now := time.Now()
	return &TokenBucket{
		Capacity:   capacity,
		Tokens:     capacity, // Start with full bucket
		RefillRate: refillRate,
		LastRefill: now,
		LastSeen:   now,
	}
}

// Allow checks if a request is allowed (consumes one token if available)
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Update LastSeen on every call (for cleanup tracking)
	now := time.Now()
	tb.LastSeen = now

	// Refill tokens based on elapsed time
	elapsed := now.Sub(tb.LastRefill)
	tokensToAdd := int(elapsed / tb.RefillRate)

	if tokensToAdd > 0 {
		tb.Tokens += tokensToAdd
		if tb.Tokens > tb.Capacity {
			tb.Tokens = tb.Capacity
		}
		tb.LastRefill = now
	}

	// Check if we have tokens available
	if tb.Tokens > 0 {
		tb.Tokens--
		return true
	}

	return false
}

// RateLimiter manages rate limiting for multiple keys (API keys, IPs, users, etc.)
// Supports optional Redis-backed distributed rate limiting for multi-replica deployments.
// When Redis is enabled, uses a sliding window counter (INCR + EXPIRE) for distributed
// rate limiting. Falls back to local token bucket when Redis is unavailable.
type RateLimiter struct {
	limiters   map[string]*TokenBucket // key -> token bucket (local fallback)
	mu         sync.RWMutex
	capacity   int           // Tokens per window
	refillRate time.Duration // Refill rate (e.g., 1 token per second)
	log        logr.Logger
	cleanupTTL time.Duration // TTL for inactive buckets (default: 10 minutes)

	// Redis-backed distributed rate limiting (optional)
	redis        *redis.Client
	redisEnabled bool
	redisTimeout time.Duration
	windowSize   time.Duration // Sliding window size for Redis counter
}

// #nosec G101 -- Redis Lua script source, not a credential.
const redisTokenBucketScript = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_micros = tonumber(ARGV[2])
local now_micros = tonumber(ARGV[3])
local ttl_seconds = tonumber(ARGV[4])

if capacity == nil or capacity <= 0 or refill_micros == nil or refill_micros <= 0 then
	return redis.error_reply("invalid token bucket configuration")
end

local tokens = capacity
local last_refill = now_micros
local raw = redis.call("GET", key)
if raw then
	local sep = string.find(raw, ":")
	if sep then
		local parsed_tokens = tonumber(string.sub(raw, 1, sep - 1))
		local parsed_last = tonumber(string.sub(raw, sep + 1))
		if parsed_tokens ~= nil and parsed_last ~= nil then
			tokens = parsed_tokens
			last_refill = parsed_last
		end
	end
end

local elapsed = now_micros - last_refill
if elapsed < 0 then
	elapsed = 0
end

local tokens_to_add = math.floor(elapsed / refill_micros)
if tokens_to_add > 0 then
	tokens = tokens + tokens_to_add
	if tokens > capacity then
		tokens = capacity
	end
	last_refill = now_micros
end

local allowed = 0
if tokens > 0 then
	tokens = tokens - 1
	allowed = 1
end

redis.call("SET", key, tostring(tokens) .. ":" .. tostring(last_refill), "EX", ttl_seconds)
return allowed
`

// NewRateLimiter creates a new rate limiter with local-only token buckets
func NewRateLimiter(capacity int, refillRate time.Duration, log logr.Logger) *RateLimiter {
	return &RateLimiter{
		limiters:   make(map[string]*TokenBucket),
		capacity:   capacity,
		refillRate: refillRate,
		log:        log,
		cleanupTTL: 10 * time.Minute,
	}
}

// NewDistributedRateLimiter creates a rate limiter with Redis-backed distributed counting.
// Falls back to local token bucket when Redis is unavailable.
func NewDistributedRateLimiter(capacity int, refillRate time.Duration, redisClient *redis.Client, redisTimeout time.Duration, log logr.Logger) *RateLimiter {
	rl := NewRateLimiter(capacity, refillRate, log)
	if redisClient != nil {
		rl.redis = redisClient
		rl.redisEnabled = true
		rl.redisTimeout = redisTimeout
		// Window size = capacity * refillRate (time for full bucket to drain)
		rl.windowSize = time.Duration(capacity) * refillRate
		if rl.windowSize < time.Second {
			rl.windowSize = time.Second
		}
		log.Info("Distributed rate limiting enabled via Redis", "capacity", capacity, "windowSize", rl.windowSize)
	}
	return rl
}

// StartCleanup starts a background goroutine to clean up inactive buckets
// This prevents memory leaks from accumulating rate limit buckets
func (rl *RateLimiter) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.cleanup()
		}
	}
}

// cleanup removes inactive buckets to prevent memory leaks
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	deleted := 0
	for key, bucket := range rl.limiters {
		bucket.mu.Lock()
		inactive := now.Sub(bucket.LastSeen) // Use LastSeen instead of LastRefill
		bucket.mu.Unlock()

		if inactive > rl.cleanupTTL {
			delete(rl.limiters, key)
			deleted++
		}
	}

	if deleted > 0 {
		rl.log.V(1).Info("Cleaned up inactive rate limit buckets", "deleted", deleted, "remaining", len(rl.limiters))
	}
}

// Allow checks if a request is allowed for the given key.
// Uses Redis distributed counter when available, falls back to local token bucket.
// Uses RLock fast path for existing local buckets to avoid serializing all clients.
func (rl *RateLimiter) Allow(key string) bool {
	// Try Redis distributed rate limiting first (if enabled)
	if rl.redisEnabled && rl.redis != nil {
		allowed, err := rl.allowRedis(key)
		if err == nil {
			return allowed
		}
		// Redis error - fall through to local token bucket
		rl.log.V(1).Info("Redis rate limit failed, falling back to local", "key", key, "error", err)
	}

	// Local token bucket (fast path with RLock for existing buckets)
	rl.mu.RLock()
	bucket, exists := rl.limiters[key]
	rl.mu.RUnlock()

	if exists {
		return bucket.Allow()
	}

	// Slow path: Lock to create new bucket (rare case - first request per key)
	rl.mu.Lock()
	// Double-check after acquiring write lock (another goroutine may have created it)
	bucket, exists = rl.limiters[key]
	if !exists {
		bucket = NewTokenBucket(rl.capacity, rl.refillRate)
		rl.limiters[key] = bucket
	}
	rl.mu.Unlock()

	return bucket.Allow()
}

// allowRedis implements distributed rate limiting as a Redis-backed token bucket.
// The semantics intentionally match the local TokenBucket: a full initial burst,
// then one token refilled per refillRate.
func (rl *RateLimiter) allowRedis(key string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rl.redisTimeout)
	defer cancel()

	refillMicros := rl.refillRate.Microseconds()
	if refillMicros <= 0 {
		refillMicros = int64(time.Second / time.Microsecond)
	}
	ttlSeconds := redisTTLSeconds(rl.windowSize + time.Second)
	redisKey := fmt.Sprintf("rl:%s", key)

	allowed, err := rl.redis.Eval(
		ctx,
		redisTokenBucketScript,
		[]string{redisKey},
		rl.capacity,
		refillMicros,
		time.Now().UnixMicro(),
		ttlSeconds,
	).Int64()
	if err != nil {
		return false, err
	}

	return allowed == 1, nil
}

func redisTTLSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 1
	}
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return int64(seconds)
}

// extractRateLimitKey extracts the rate limit key from the request.
// Priority: RemoteAddr > X-Forwarded-For only when RemoteAddr is unavailable.
// Raw auth headers are intentionally ignored here because this middleware runs
// before authentication; trusting presented credentials would let unauthenticated
// clients bypass IP limits by rotating fake keys or bearer tokens.
func extractRateLimitKey(r *http.Request) string {
	if ip := extractIPFromAddr(r.RemoteAddr); ip != "" {
		return "ip:" + ip
	}

	if ip := extractForwardedForIP(r.Header.Get("X-Forwarded-For")); ip != "" {
		return "ip:" + ip
	}

	return "unknown"
}

func extractForwardedForIP(xff string) string {
	if xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

// RateLimitMiddleware creates HTTP middleware for rate limiting
func RateLimitMiddleware(rateLimiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractRateLimitKey(r)

			if !rateLimiter.Allow(key) {
				// Rate limit exceeded
				w.Header().Set("X-RateLimit-Limit", "exceeded")
				w.Header().Set("Retry-After", "60") // Suggest retry after 60 seconds
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			// Request allowed, continue
			next.ServeHTTP(w, r)
		})
	}
}
