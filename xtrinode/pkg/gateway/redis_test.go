package gateway

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/redis/go-redis/v9"
)

type redisRESPTestServer struct {
	listener net.Listener
	values   map[string]string
	counts   map[string]int64
	mu       sync.Mutex
}

func startRedisRESPTestServer(t *testing.T) *redisRESPTestServer {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for Redis test server: %v", err)
	}

	server := &redisRESPTestServer{
		listener: listener,
		values:   make(map[string]string),
		counts:   make(map[string]int64),
	}

	go server.serve()
	t.Cleanup(func() {
		_ = listener.Close()
	})

	return server
}

func (s *redisRESPTestServer) addr() string {
	return s.listener.Addr().String()
}

func TestRedactURLForLog(t *testing.T) {
	got := redactURLForLog("redis://user:pass@example.com:6379/0?token=abc&mode=prod")
	if strings.Contains(got, "pass") || strings.Contains(got, "abc") || strings.Contains(got, "user:") {
		t.Fatalf("redacted URL leaked sensitive material: %s", got)
	}
	if !strings.Contains(got, "example.com:6379") {
		t.Fatalf("redacted URL should preserve host, got %s", got)
	}
	if !strings.Contains(got, "token=REDACTED") {
		t.Fatalf("redacted URL should redact token query parameter, got %s", got)
	}
}

func (s *redisRESPTestServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *redisRESPTestServer) handleConn(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPArray(reader)
		if err != nil {
			return
		}
		s.handleCommand(conn, args)
	}
}

func readRESPArray(reader *bufio.Reader) ([]string, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if prefix != '*' {
		return nil, io.ErrUnexpectedEOF
	}

	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if b, err := reader.ReadByte(); err != nil || b != '$' {
			return nil, io.ErrUnexpectedEOF
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}

	return args, nil
}

func (s *redisRESPTestServer) handleCommand(conn net.Conn, args []string) {
	if len(args) == 0 {
		_, _ = conn.Write([]byte("-ERR empty command\r\n"))
		return
	}

	switch strings.ToUpper(args[0]) {
	case "HELLO":
		_, _ = conn.Write([]byte("-ERR unknown command 'HELLO'\r\n"))
	case "CLIENT", "SELECT", "AUTH", "EXPIRE":
		_, _ = conn.Write([]byte("+OK\r\n"))
	case "PING":
		_, _ = conn.Write([]byte("+PONG\r\n"))
	case "SET":
		if len(args) < 3 {
			_, _ = conn.Write([]byte("-ERR wrong number of arguments\r\n"))
			return
		}
		s.mu.Lock()
		s.values[args[1]] = args[2]
		s.mu.Unlock()
		_, _ = conn.Write([]byte("+OK\r\n"))
	case "GET":
		s.mu.Lock()
		value, ok := s.values[args[1]]
		s.mu.Unlock()
		if !ok {
			_, _ = conn.Write([]byte("$-1\r\n"))
			return
		}
		_, _ = conn.Write([]byte("$" + strconv.Itoa(len(value)) + "\r\n" + value + "\r\n"))
	case "DEL":
		s.mu.Lock()
		_, existed := s.values[args[1]]
		delete(s.values, args[1])
		s.mu.Unlock()
		if existed {
			_, _ = conn.Write([]byte(":1\r\n"))
		} else {
			_, _ = conn.Write([]byte(":0\r\n"))
		}
	case "INCR":
		s.mu.Lock()
		s.counts[args[1]]++
		count := s.counts[args[1]]
		s.mu.Unlock()
		_, _ = conn.Write([]byte(":" + strconv.FormatInt(count, 10) + "\r\n"))
	case "EVAL":
		allowed, err := s.handleEval(args)
		if err != nil {
			_, _ = conn.Write([]byte("-ERR " + err.Error() + "\r\n"))
			return
		}
		_, _ = conn.Write([]byte(":" + strconv.FormatInt(allowed, 10) + "\r\n"))
	default:
		_, _ = conn.Write([]byte("-ERR unsupported command\r\n"))
	}
}

func (s *redisRESPTestServer) handleEval(args []string) (int64, error) {
	if len(args) < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	numKeys, err := strconv.Atoi(args[2])
	if err != nil || numKeys != 1 {
		return 0, io.ErrUnexpectedEOF
	}

	key := args[3]
	capacity, err := strconv.ParseInt(args[4], 10, 64)
	if err != nil || capacity <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	refillMicros, err := strconv.ParseInt(args[5], 10, 64)
	if err != nil || refillMicros <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	nowMicros, err := strconv.ParseInt(args[6], 10, 64)
	if err != nil {
		return 0, io.ErrUnexpectedEOF
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tokens := capacity
	lastRefill := nowMicros
	if raw, ok := s.values[key]; ok {
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) == 2 {
			parsedTokens, tokenErr := strconv.ParseInt(parts[0], 10, 64)
			parsedLastRefill, refillErr := strconv.ParseInt(parts[1], 10, 64)
			if tokenErr == nil && refillErr == nil {
				tokens = parsedTokens
				lastRefill = parsedLastRefill
			}
		}
	}

	elapsed := nowMicros - lastRefill
	if elapsed < 0 {
		elapsed = 0
	}
	if tokensToAdd := elapsed / refillMicros; tokensToAdd > 0 {
		tokens += tokensToAdd
		if tokens > capacity {
			tokens = capacity
		}
		lastRefill = nowMicros
	}

	var allowed int64
	if tokens > 0 {
		tokens--
		allowed = 1
	}
	s.values[key] = strconv.FormatInt(tokens, 10) + ":" + strconv.FormatInt(lastRefill, 10)
	return allowed, nil
}

func TestNewRedisStickyClientDisabledDefaultsAndNoopMethods(t *testing.T) {
	sticky, err := NewRedisStickyClient(RedisConfig{
		Enabled: false,
		TTL:     0,
	}, logr.Discard())
	if err != nil {
		t.Fatalf("expected disabled Redis client to initialize: %v", err)
	}
	if sticky.IsEnabled() {
		t.Fatal("expected Redis to be disabled")
	}
	if sticky.config.TTL <= 0 {
		t.Fatal("expected default sticky TTL to be applied")
	}
	if err := sticky.HealthCheck(context.Background()); err == nil || !strings.Contains(err.Error(), "redis not enabled") {
		t.Fatalf("expected redis not enabled health error, got %v", err)
	}
	if err := sticky.Close(); err != nil {
		t.Fatalf("expected nil Redis close to be a no-op: %v", err)
	}
}

func TestNewRedisStickyClientEnabledRequiresURL(t *testing.T) {
	_, err := NewRedisStickyClient(RedisConfig{
		Enabled: true,
		TTL:     time.Minute,
	}, logr.Discard())
	if err == nil {
		t.Fatal("expected missing Redis URL to fail")
	}
	if !strings.Contains(err.Error(), "redis URL is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRedisStickyClientInvalidURL(t *testing.T) {
	_, err := NewRedisStickyClient(RedisConfig{
		Enabled: true,
		URL:     "://bad-url",
		TTL:     time.Minute,
	}, logr.Discard())
	if err == nil {
		t.Fatal("expected invalid Redis URL to fail")
	}
	if !strings.Contains(err.Error(), "failed to parse Redis URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRedisStickyClientEnabledRequiresReachableRedis(t *testing.T) {
	_, err := NewRedisStickyClient(RedisConfig{
		Enabled: true,
		URL:     "redis://127.0.0.1:1?protocol=2",
		TTL:     time.Minute,
		Timeout: 10 * time.Millisecond,
	}, logr.Discard())
	if err == nil {
		t.Fatal("expected unreachable Redis to fail startup")
	}
	if !strings.Contains(err.Error(), "failed to connect to Redis") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRedisStickyClientUsesRedisWhenAvailable(t *testing.T) {
	server := startRedisRESPTestServer(t)
	sticky, err := NewRedisStickyClient(RedisConfig{
		Enabled: true,
		URL:     "redis://" + server.addr() + "?protocol=2",
		TTL:     time.Minute,
		Timeout: time.Second,
	}, logr.Discard())
	if err != nil {
		t.Fatalf("expected Redis sticky client to initialize: %v", err)
	}
	defer sticky.Close()

	if !sticky.IsEnabled() {
		t.Fatal("expected Redis to be enabled")
	}
	if err := sticky.HealthCheck(context.Background()); err != nil {
		t.Fatalf("expected Redis health check to pass: %v", err)
	}

	queryID := "20250115_123456_00005_abc12"
	if err := sticky.Set(context.Background(), queryID, "team-a", "runtime-a", "http://backend:8080", "runtime-a"); err != nil {
		t.Fatalf("set sticky route in Redis: %v", err)
	}

	ns, name, backendURL, routingGroup, found := sticky.Get(context.Background(), queryID)
	if !found {
		t.Fatal("expected Redis sticky route to be found")
	}
	if ns != "team-a" || name != "runtime-a" || backendURL != "http://backend:8080" || routingGroup != "runtime-a" {
		t.Fatalf("unexpected sticky route: %s/%s %s %s", ns, name, backendURL, routingGroup)
	}

	if err := sticky.Delete(context.Background(), queryID); err != nil {
		t.Fatalf("delete sticky route from Redis: %v", err)
	}
	_, _, _, _, found = sticky.Get(context.Background(), queryID) //nolint:dogsled // test only checks the found flag
	if found {
		t.Fatal("expected deleted Redis sticky route to be absent")
	}
}

func TestRedisStickyClientRedisMissDoesNotUseFallback(t *testing.T) {
	server := startRedisRESPTestServer(t)
	sticky, err := NewRedisStickyClient(RedisConfig{
		Enabled: true,
		URL:     "redis://" + server.addr() + "?protocol=2",
		TTL:     time.Minute,
		Timeout: time.Second,
	}, logr.Discard())
	if err != nil {
		t.Fatalf("expected Redis sticky client to initialize: %v", err)
	}
	defer sticky.Close()

	queryID := "20250115_123456_00006_abc12"
	sticky.fallbackCache.Add(queryID, StickyRoute{
		Namespace:    "team-a",
		Name:         "stale",
		BackendURL:   "http://stale:8080",
		RoutingGroup: "runtime-a",
	})

	_, _, _, _, found := sticky.Get(context.Background(), queryID) //nolint:dogsled // test only checks found flag
	if found {
		t.Fatal("expected Redis miss to ignore stale fallback cache")
	}
	if _, ok := sticky.fallbackCache.Get(queryID); ok {
		t.Fatal("expected Redis miss to clear stale fallback cache entry")
	}
}

func TestRedisStickyClientDeleteIgnoresRedisErrorAndClearsFallback(t *testing.T) {
	sticky, err := NewRedisStickyClient(RedisConfig{
		Enabled: false,
		TTL:     time.Minute,
		Timeout: 10 * time.Millisecond,
	}, logr.Discard())
	if err != nil {
		t.Fatalf("expected disabled Redis client to initialize: %v", err)
	}

	queryID := "20250115_123456_00004_abc12"
	ctx := context.Background()
	if err := sticky.Set(ctx, queryID, "team-a", "runtime-a", "http://backend:8080", "runtime-a"); err != nil {
		t.Fatalf("seed sticky route: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
	})
	defer redisClient.Close()

	sticky.redis = redisClient
	sticky.enabled = true

	deleteCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := sticky.Delete(deleteCtx, queryID); err != nil {
		t.Fatalf("delete should ignore Redis errors after clearing fallback: %v", err)
	}

	getCtx, getCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer getCancel()
	_, _, _, _, found := sticky.Get(getCtx, queryID) //nolint:dogsled // test only checks the found flag
	if found {
		t.Fatal("expected fallback sticky route to be removed")
	}
}

func TestRedisStickyClientHealthCheckReturnsRedisPingError(t *testing.T) {
	sticky, err := NewRedisStickyClient(RedisConfig{
		Enabled: false,
		TTL:     time.Minute,
		Timeout: 10 * time.Millisecond,
	}, logr.Discard())
	if err != nil {
		t.Fatalf("expected disabled Redis client to initialize: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
	})
	defer redisClient.Close()

	sticky.redis = redisClient
	sticky.enabled = true

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := sticky.HealthCheck(ctx); err == nil {
		t.Fatal("expected Redis ping error")
	}
}
