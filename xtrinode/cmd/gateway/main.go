package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8sconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/cmdflags"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/pkg/gateway"
	"github.com/xtrinode/xtrinode/pkg/gateway/auth"
)

var (
	// Version information (set via ldflags during build)
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"

	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(analyticsv1.AddToScheme(scheme))
}

func main() {
	os.Exit(run())
}

type gatewayOptions struct {
	apiServerURL             string
	gatewayPort              int
	logLevel                 string
	showVersion              bool
	apiServerAuthTokenFile   string
	authEnabled              bool
	authType                 string
	authSecretName           string
	authSecretKey            string
	authNamespace            string
	authOAuthIssuer          string
	authOAuthAudience        string
	authOAuthJWKSURL         string
	authOAuthRefreshInterval time.Duration
	redisEnabled             bool
	redisURL                 string
	redisPassword            string
	redisDB                  int
	redisStickyTTL           time.Duration
	redisTimeout             time.Duration
	rateLimitEnabled         bool
	rateLimitCapacity        int
	rateLimitRefillRate      time.Duration
	readHeaderTimeout        time.Duration
	readTimeout              time.Duration
	writeTimeout             time.Duration
	idleTimeout              time.Duration
}

func defaultGatewayOptions() gatewayOptions {
	return gatewayOptions{
		apiServerURL:             config.BuildAPIServerServiceURL(config.OperatorDefaultNamespace),
		gatewayPort:              config.GatewayPort,
		logLevel:                 config.DefaultLogLevel,
		authType:                 "api-key",
		authSecretName:           config.GatewayAuthSecretName,
		authSecretKey:            config.GatewayAuthSecretKey,
		authNamespace:            config.GatewayDefaultNamespace,
		authOAuthRefreshInterval: 1 * time.Hour,
		redisEnabled:             config.GatewayRedisEnabled,
		redisURL:                 config.GatewayRedisURL,
		redisPassword:            config.GatewayRedisPassword,
		redisDB:                  config.GatewayRedisDB,
		redisStickyTTL:           config.GatewayRedisStickyTTL,
		redisTimeout:             config.GatewayRedisTimeout,
		rateLimitEnabled:         true,
		rateLimitCapacity:        config.GatewayRateLimitCapacity,
		rateLimitRefillRate:      config.GatewayRateLimitRefillRate,
		readHeaderTimeout:        config.GatewayReadHeaderTimeout,
		readTimeout:              config.GatewayReadTimeout,
		writeTimeout:             config.GatewayWriteTimeout,
		idleTimeout:              config.GatewayIdleTimeout,
	}
}

func parseGatewayOptions(args []string, output io.Writer) (gatewayOptions, zap.Options, error) {
	options := defaultGatewayOptions()
	zapOptions := zap.Options{}
	fs := flag.NewFlagSet("xtrinode-gateway", flag.ContinueOnError)
	if output != nil {
		fs.SetOutput(output)
	}

	fs.StringVar(&options.apiServerURL, "api-server-url", options.apiServerURL, "API server base URL for resume requests")
	fs.IntVar(&options.gatewayPort, "gateway-port", options.gatewayPort, "Gateway HTTP listen port")
	fs.StringVar(&options.logLevel, "log-level", options.logLevel, "Log level (debug, info, error)")
	fs.BoolVar(&options.showVersion, "version", false, "Show version information and exit")
	fs.StringVar(&options.apiServerAuthTokenFile, "api-server-auth-token-file", "", "Path to file containing the API server bearer token; falls back to XTRINODE_API_SERVER_AUTH_TOKEN when empty")
	fs.BoolVar(&options.authEnabled, "auth-enabled", false, "Enable authentication")
	fs.StringVar(&options.authType, "auth-type", options.authType, "Authentication type (api-key, oauth, oidc, bearer-token, jwt, none)")
	fs.StringVar(&options.authSecretName, "auth-secret-name", options.authSecretName, "Kubernetes Secret name for authentication")
	fs.StringVar(&options.authSecretKey, "auth-secret-key", options.authSecretKey, "Key in Secret containing authentication data")
	fs.StringVar(&options.authNamespace, "auth-namespace", options.authNamespace, "Namespace where authentication Secret is located")
	fs.StringVar(&options.authOAuthIssuer, "auth-oauth-issuer", "", "OAuth/OIDC issuer URL for bearer-token authentication")
	fs.StringVar(&options.authOAuthAudience, "auth-oauth-audience", "", "OAuth/OIDC expected audience for bearer-token authentication")
	fs.StringVar(&options.authOAuthJWKSURL, "auth-oauth-jwks-url", "", "OAuth/OIDC JWKS URL for bearer-token authentication; discovered from issuer when empty")
	fs.DurationVar(&options.authOAuthRefreshInterval, "auth-oauth-refresh-interval", options.authOAuthRefreshInterval, "OAuth/OIDC JWKS refresh interval")
	fs.BoolVar(&options.redisEnabled, "redis-enabled", options.redisEnabled, "Enable Redis for gateway sticky routing and distributed rate limiting")
	fs.StringVar(&options.redisURL, "redis-url", options.redisURL, "Redis URL for gateway sticky routing and distributed rate limiting")
	fs.StringVar(&options.redisPassword, "redis-password", options.redisPassword, "Redis password")
	fs.IntVar(&options.redisDB, "redis-db", options.redisDB, "Redis database number")
	fs.DurationVar(&options.redisStickyTTL, "redis-sticky-ttl", options.redisStickyTTL, "Redis sticky query routing TTL")
	fs.DurationVar(&options.redisTimeout, "redis-timeout", options.redisTimeout, "Redis operation timeout")
	fs.BoolVar(&options.rateLimitEnabled, "rate-limit-enabled", options.rateLimitEnabled, "Enable gateway request rate limiting")
	fs.IntVar(&options.rateLimitCapacity, "rate-limit-capacity", options.rateLimitCapacity, "Gateway rate limit capacity per key")
	fs.DurationVar(&options.rateLimitRefillRate, "rate-limit-refill-rate", options.rateLimitRefillRate, "Gateway rate limit token refill interval")
	fs.DurationVar(&options.readHeaderTimeout, "read-header-timeout", options.readHeaderTimeout, "HTTP read-header timeout (protects against slow headers)")
	fs.DurationVar(&options.readTimeout, "read-timeout", options.readTimeout, "HTTP read timeout; 0 disables the deadline for Trino request streams")
	fs.DurationVar(&options.writeTimeout, "write-timeout", options.writeTimeout, "HTTP write timeout; 0 disables the deadline for Trino response streams")
	fs.DurationVar(&options.idleTimeout, "idle-timeout", options.idleTimeout, "HTTP keep-alive idle timeout")

	zapOptions.BindFlags(fs)
	if err := fs.Parse(args); err != nil {
		return options, zapOptions, err
	}
	if err := cmdflags.ApplyLogLevelFlag(fs, options.logLevel, &zapOptions); err != nil {
		return options, zapOptions, err
	}
	return options, zapOptions, nil
}

func run() int {
	options, zapOptions, err := parseGatewayOptions(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if options.showVersion {
		fmt.Printf("xtrinode-gateway version %s (commit: %s, built: %s)\n", version, commit, buildDate)
		return 0
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	log := ctrl.Log.WithName("gateway")

	log.Info("Starting XTrinode Gateway", "apiServerURL", options.apiServerURL, "version", version, "commit", commit, "buildDate", buildDate)

	apiServerAuthToken, err := loadBearerToken(options.apiServerAuthTokenFile, "XTRINODE_API_SERVER_AUTH_TOKEN")
	if err != nil {
		log.Error(err, "invalid API server auth configuration")
		return 1
	}

	// Get Kubernetes client
	cfg, err := k8sconfig.GetConfig()
	if err != nil {
		log.Error(err, "unable to get kubeconfig")
		return 1
	}

	cli, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "unable to create kubernetes client")
		return 1
	}

	// Initialize authenticator if enabled
	var authenticator auth.Authenticator
	if options.authEnabled {
		switch options.authType {
		case "api-key":
			apiKeyConfig := &auth.APIKeyConfig{
				SecretName: options.authSecretName,
				SecretKey:  options.authSecretKey,
				Namespace:  options.authNamespace,
			}

			apiKeyAuth, authErr := auth.NewAPIKeyAuthenticator(cli, log, apiKeyConfig)
			if authErr != nil {
				log.Error(authErr, "unable to create API key authenticator")
				return 1
			}

			authenticator = apiKeyAuth
			log.Info("API key authentication enabled", "secret", options.authSecretName, "namespace", options.authNamespace)
		case "oauth", "oidc", "bearer-token", "jwt":
			oauthConfig := &auth.BearerTokenConfig{
				Issuer:          options.authOAuthIssuer,
				Audience:        options.authOAuthAudience,
				JWKSUrl:         options.authOAuthJWKSURL,
				RefreshInterval: options.authOAuthRefreshInterval,
			}

			oauthAuth, authErr := auth.NewBearerTokenAuthenticator(log, oauthConfig)
			if authErr != nil {
				log.Error(authErr, "unable to create OAuth bearer token authenticator")
				return 1
			}

			authenticator = oauthAuth
			log.Info("OAuth bearer token authentication enabled", "issuer", options.authOAuthIssuer, "audience", options.authOAuthAudience)
		case "none":
			log.Info("Authentication explicitly disabled by auth type")
		default:
			log.Error(fmt.Errorf("unsupported auth type"), "authentication type not supported", "type", options.authType)
			return 1
		}
	} else {
		log.Info("Authentication disabled")
	}

	// Create gateway service
	gatewayService, err := gateway.NewGatewayServiceWithOptions(
		cli,
		log,
		options.apiServerURL,
		authenticator,
		&gateway.GatewayOptions{
			APIServerAuthToken: apiServerAuthToken,
			Port:               options.gatewayPort,
			Redis: gateway.RedisConfig{
				Enabled:  options.redisEnabled,
				URL:      options.redisURL,
				Password: options.redisPassword,
				DB:       options.redisDB,
				TTL:      options.redisStickyTTL,
				Timeout:  options.redisTimeout,
			},
			RateLimit: gateway.RateLimitConfig{
				Enabled:    options.rateLimitEnabled,
				Capacity:   options.rateLimitCapacity,
				RefillRate: options.rateLimitRefillRate,
			},
			HTTPServer: gateway.HTTPServerConfig{
				ReadHeaderTimeout: options.readHeaderTimeout,
				ReadTimeout:       options.readTimeout,
				WriteTimeout:      options.writeTimeout,
				IdleTimeout:       options.idleTimeout,
			},
		},
	)
	if err != nil {
		log.Error(err, "unable to create gateway service")
		return 1
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start authenticator if it implements Startable interface
	if startable, ok := authenticator.(auth.Startable); ok {
		if err := startable.Start(ctx); err != nil {
			log.Error(err, "unable to start authenticator")
			return 1
		}
	}

	// Start gateway in goroutine
	errChan := make(chan error, 1)
	doneChan := make(chan struct{}, 1)
	go func() {
		defer close(doneChan)
		if err := gatewayService.Start(ctx); err != nil {
			errChan <- fmt.Errorf("gateway service error: %w", err)
		}
	}()

	// Wait for signal or error
	select {
	case <-sigChan:
		log.Info("Received shutdown signal, shutting down gracefully")
		cancel()
		// Wait for gateway service to finish graceful shutdown
		select {
		case <-doneChan:
			// Always check errChan after doneChan to avoid swallowing errors.
			select {
			case err := <-errChan:
				if err != nil {
					log.Error(err, "Gateway service error during shutdown")
					return 1
				}
			default:
				// No error in errChan - clean shutdown
			}
			log.Info("Gateway service stopped gracefully")
		case err := <-errChan:
			if err != nil {
				log.Error(err, "Gateway service error during shutdown")
				return 1
			}
		case <-time.After(config.GatewayShutdownWaitTimeout):
			log.Error(fmt.Errorf("shutdown timeout"), "Gateway service did not stop within timeout")
			return 1
		}
	case err := <-errChan:
		if err != nil {
			log.Error(err, "Gateway service error")
			cancel()
			return 1
		}
	}

	log.Info("Gateway service stopped")
	return 0
}

func loadBearerToken(tokenFile, envName string) (string, error) {
	token := strings.TrimSpace(os.Getenv(envName))
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("failed to read auth token file: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}
	return token, nil
}
