package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/redis/go-redis/v9"
	"github.com/xtrinode/xtrinode/internal/config"
)

// StickyRoute represents a sticky routing entry
type StickyRoute struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	BackendURL   string `json:"backendURL"`
	RoutingGroup string `json:"routingGroup"`
}

// RedisConfig holds Redis client configuration
type RedisConfig struct {
	Enabled  bool
	URL      string
	Password string
	DB       int
	TTL      time.Duration
	Timeout  time.Duration
}

// RedisStickyClient manages sticky routing with Redis backend and local fallback
type RedisStickyClient struct {
	redis         *redis.Client
	fallbackCache *expirable.LRU[string, StickyRoute]
	config        RedisConfig
	log           logr.Logger
	enabled       bool
}

// NewRedisStickyClient creates a new Redis sticky routing client with fallback.
// When Redis is explicitly enabled, startup requires a successful Redis ping so
// multi-replica gateways do not silently run with per-pod local state.
func NewRedisStickyClient(cfg RedisConfig, log logr.Logger) (*RedisStickyClient, error) {
	// Enforce default TTL to prevent unbounded Redis growth
	// If TTL is 0, Redis SET with TTL=0 means "no expiry" which will leak keys
	if cfg.TTL <= 0 {
		cfg.TTL = config.GatewayRedisStickyTTL
		log.Info("Redis TTL not configured or invalid, using default", "ttl", cfg.TTL)
	}

	client := &RedisStickyClient{
		config:  cfg,
		log:     log,
		enabled: cfg.Enabled,
	}

	// Create fallback LRU cache (TTL matches Redis)
	// NOTE: expirable.LRU is thread-safe per hashicorp/golang-lru/v2 documentation
	// No additional mutex wrapper needed for concurrent access
	client.fallbackCache = expirable.NewLRU[string, StickyRoute](config.GatewayRedisFallbackCacheSize, nil, cfg.TTL)

	// Initialize Redis client if enabled
	if cfg.Enabled {
		if cfg.URL == "" {
			return nil, fmt.Errorf("redis URL is required when Redis is enabled")
		}

		opts, err := redis.ParseURL(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
		}

		// Override password if provided
		if cfg.Password != "" {
			opts.Password = cfg.Password
		}

		// Override DB if provided
		if cfg.DB > 0 {
			opts.DB = cfg.DB
		}

		// Set timeouts
		if cfg.Timeout > 0 {
			opts.DialTimeout = cfg.Timeout
			opts.ReadTimeout = cfg.Timeout
			opts.WriteTimeout = cfg.Timeout
		}

		client.redis = redis.NewClient(opts)

		// Test connection
		ctx, cancel := context.WithTimeout(context.Background(), config.GatewayRedisPingTimeout)
		defer cancel()

		if err := client.redis.Ping(ctx).Err(); err != nil {
			recordRedisFallback("connection_error")
			if closeErr := client.redis.Close(); closeErr != nil {
				log.V(1).Info("Failed to close Redis client after startup ping failure", "error", closeErr)
			}
			return nil, fmt.Errorf("failed to connect to Redis: %w", err)
		}

		log.Info("Redis sticky routing enabled", "url", redactURLForLog(cfg.URL), "db", opts.DB)
	} else {
		log.Info("Redis sticky routing disabled, using local cache only")
	}

	return client, nil
}

func redactURLForLog(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "password") ||
			strings.Contains(lowerKey, "token") ||
			strings.Contains(lowerKey, "secret") {
			query.Set(key, "REDACTED")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// Set stores a sticky route mapping
func (r *RedisStickyClient) Set(ctx context.Context, queryID, namespace, name, backendURL, routingGroup string) error {
	route := StickyRoute{
		Namespace:    namespace,
		Name:         name,
		BackendURL:   backendURL,
		RoutingGroup: routingGroup,
	}

	// Always update fallback cache
	r.fallbackCache.Add(queryID, route)

	// Try Redis if enabled
	if r.enabled && r.redis != nil {
		key := makeRedisKey(queryID)
		value, err := json.Marshal(route)
		if err != nil {
			r.log.Error(err, "Failed to marshal sticky route", "queryID", queryID)
			recordRedisError("marshal")
			return err
		}

		err = r.redis.Set(ctx, key, value, r.config.TTL).Err()
		if err != nil {
			r.log.V(1).Info("Failed to set Redis key, using fallback cache only",
				"queryID", queryID,
				"error", err)
			recordRedisError("set")
			// Don't return error - fallback cache is already updated
			return nil
		}

		recordRedisSet()
	}

	return nil
}

// Get retrieves a sticky route mapping
// Returns (namespace, name, backendURL, routingGroup, found)
func (r *RedisStickyClient) Get(ctx context.Context, queryID string) (namespace, name, backendURL, routingGroup string, found bool) {
	// Try Redis first if enabled
	if r.enabled && r.redis != nil {
		key := makeRedisKey(queryID)
		value, err := r.redis.Get(ctx, key).Result()

		if err == nil {
			// Redis hit
			var route StickyRoute
			if unmarshalErr := json.Unmarshal([]byte(value), &route); unmarshalErr != nil {
				r.log.Error(unmarshalErr, "Failed to unmarshal sticky route from Redis", "queryID", queryID)
				recordRedisError("unmarshal")
				r.fallbackCache.Remove(queryID)
				return "", "", "", "", false
			} else {
				recordRedisHit()
				// Update fallback cache with Redis value
				r.fallbackCache.Add(queryID, route)
				return route.Namespace, route.Name, route.BackendURL, route.RoutingGroup, true
			}
		} else if err != redis.Nil {
			// Redis error (not just miss)
			r.log.V(1).Info("Redis GET error, trying fallback cache",
				"queryID", queryID,
				"error", err)
			recordRedisError("get")
		} else {
			// Redis is authoritative when available. Do not resurrect a deleted
			// distributed sticky route from this replica's local fallback cache.
			recordRedisMiss()
			r.fallbackCache.Remove(queryID)
			return "", "", "", "", false
		}
	}

	// Try fallback cache
	if route, ok := r.fallbackCache.Get(queryID); ok {
		recordFallbackHit()
		return route.Namespace, route.Name, route.BackendURL, route.RoutingGroup, true
	}

	recordFallbackMiss()
	return "", "", "", "", false
}

// Delete removes a sticky route mapping
func (r *RedisStickyClient) Delete(ctx context.Context, queryID string) error {
	// Remove from fallback cache
	r.fallbackCache.Remove(queryID)

	// Try Redis if enabled
	if r.enabled && r.redis != nil {
		key := makeRedisKey(queryID)
		err := r.redis.Del(ctx, key).Err()
		if err != nil {
			r.log.V(1).Info("Failed to delete Redis key",
				"queryID", queryID,
				"error", err)
			recordRedisError("delete")
			// Don't return error - fallback cache is already cleared
		}
	}

	return nil
}

// Close closes the Redis connection
func (r *RedisStickyClient) Close() error {
	if r.redis != nil {
		return r.redis.Close()
	}
	return nil
}

// HealthCheck performs a Redis health check
func (r *RedisStickyClient) HealthCheck(ctx context.Context) error {
	if !r.enabled || r.redis == nil {
		return fmt.Errorf("redis not enabled")
	}

	return r.redis.Ping(ctx).Err()
}

// IsEnabled returns whether Redis is enabled
func (r *RedisStickyClient) IsEnabled() bool {
	return r.enabled
}

// makeRedisKey creates a Redis key for a query ID
func makeRedisKey(queryID string) string {
	return "query:" + queryID
}

// Metrics recording functions (to be implemented in metrics.go)

func recordRedisSet() {
	// Increment Redis SET counter
	if gatewayRedisOperationsTotal != nil {
		gatewayRedisOperationsTotal.WithLabelValues("set", "success").Inc()
	}
}

func recordRedisHit() {
	// Increment Redis hit counter
	if gatewayRedisHitsTotal != nil {
		gatewayRedisHitsTotal.Inc()
	}
}

func recordRedisMiss() {
	// Increment Redis miss counter
	if gatewayRedisMissesTotal != nil {
		gatewayRedisMissesTotal.Inc()
	}
}

func recordRedisError(operation string) {
	// Increment Redis error counter
	if gatewayRedisErrorsTotal != nil {
		gatewayRedisErrorsTotal.WithLabelValues(operation).Inc()
	}
}

func recordRedisFallback(reason string) {
	// Increment Redis fallback counter
	if gatewayRedisFallbackTotal != nil {
		gatewayRedisFallbackTotal.WithLabelValues(reason).Inc()
	}
}

func recordFallbackHit() {
	// Increment fallback cache hit counter
	if gatewayFallbackCacheHitsTotal != nil {
		gatewayFallbackCacheHitsTotal.Inc()
	}
}

func recordFallbackMiss() {
	// Increment fallback cache miss counter
	if gatewayFallbackCacheMissesTotal != nil {
		gatewayFallbackCacheMissesTotal.Inc()
	}
}
