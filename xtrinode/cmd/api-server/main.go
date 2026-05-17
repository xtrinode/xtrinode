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

	coordinationv1 "k8s.io/api/coordination/v1"
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
	apiserver "github.com/xtrinode/xtrinode/pkg/api-server"
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
	utilruntime.Must(coordinationv1.AddToScheme(scheme))
}

func main() {
	os.Exit(run())
}

type apiServerOptions struct {
	apiPort                int
	logLevel               string
	showVersion            bool
	apiPath                string
	healthPath             string
	metricsPath            string
	readTimeoutStr         string
	writeTimeoutStr        string
	shutdownTimeoutStr     string
	resumeLeaseDurationStr string
	leaseNamespace         string
	leaseHolderIdentity    string
	authEnabled            bool
	authTokenFile          string
	resumeAuthTokenFile    string
	corsAllowedOrigins     string
}

func defaultAPIServerOptions() apiServerOptions {
	return apiServerOptions{
		apiPort:                config.APIServerPort,
		logLevel:               config.DefaultLogLevel,
		apiPath:                config.APIServerDefaultAPIPath,
		healthPath:             config.HealthPath,
		metricsPath:            config.MetricsPath,
		readTimeoutStr:         config.APIServerReadTimeout.String(),
		writeTimeoutStr:        config.APIServerWriteTimeout.String(),
		shutdownTimeoutStr:     config.APIServerShutdownTimeout.String(),
		resumeLeaseDurationStr: config.APIServerResumeLeaseDuration.String(),
		leaseNamespace:         config.APIServerDefaultLeaseNamespace,
	}
}

func parseAPIServerOptions(args []string, output io.Writer) (apiServerOptions, zap.Options, error) {
	options := defaultAPIServerOptions()
	zapOptions := zap.Options{}
	fs := flag.NewFlagSet("xtrinode-api-server", flag.ContinueOnError)
	if output != nil {
		fs.SetOutput(output)
	}

	fs.IntVar(&options.apiPort, "api-port", options.apiPort, "The port for the REST API server")
	fs.StringVar(&options.logLevel, "log-level", options.logLevel, "Log level (debug, info, error)")
	fs.BoolVar(&options.showVersion, "version", false, "Show version information and exit")
	fs.StringVar(&options.apiPath, "api-path", options.apiPath, "Base path for API endpoints")
	fs.StringVar(&options.healthPath, "health-path", options.healthPath, "Health check endpoint path")
	fs.StringVar(&options.metricsPath, "metrics-path", options.metricsPath, "Metrics endpoint path")
	fs.StringVar(&options.readTimeoutStr, "read-timeout", options.readTimeoutStr, "HTTP read timeout (e.g., 10s, 1m)")
	fs.StringVar(&options.writeTimeoutStr, "write-timeout", options.writeTimeoutStr, "HTTP write timeout (e.g., 30s, 1m)")
	fs.StringVar(&options.shutdownTimeoutStr, "shutdown-timeout", options.shutdownTimeoutStr, "Graceful shutdown timeout (e.g., 5s)")
	fs.StringVar(&options.resumeLeaseDurationStr, "resume-lease-duration", options.resumeLeaseDurationStr, "K8s Lease duration for resume gating (e.g., 120s, 2m)")
	fs.StringVar(&options.leaseNamespace, "lease-namespace", options.leaseNamespace, "Namespace for K8s Lease objects")
	fs.StringVar(&options.leaseHolderIdentity, "lease-holder-identity", "", "Identity for K8s Lease holder (default: hostname or xtrinode-api-server)")
	fs.BoolVar(&options.authEnabled, "auth-enabled", false, "Require bearer authentication for API endpoints")
	fs.StringVar(&options.authTokenFile, "auth-token-file", "", "Path to file containing the API bearer token; falls back to XTRINODE_API_SERVER_AUTH_TOKEN when empty")
	fs.StringVar(&options.resumeAuthTokenFile, "resume-auth-token-file", "", "Optional path to a resume-only bearer token; falls back to XTRINODE_API_SERVER_RESUME_AUTH_TOKEN when empty")
	fs.StringVar(&options.corsAllowedOrigins, "cors-allowed-origins", "", "Comma-separated browser origins allowed to call API endpoints; empty disables CORS")

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
	options, zapOptions, err := parseAPIServerOptions(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if options.showVersion {
		fmt.Printf("xtrinode-api-server version %s (commit: %s, built: %s)\n", version, commit, buildDate)
		return 0
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	log := ctrl.Log.WithName("api-server")

	// Parse timeout strings
	readTimeout, err := time.ParseDuration(options.readTimeoutStr)
	if err != nil {
		log.Error(err, "invalid read-timeout", "value", options.readTimeoutStr)
		return 1
	}
	writeTimeout, err := time.ParseDuration(options.writeTimeoutStr)
	if err != nil {
		log.Error(err, "invalid write-timeout", "value", options.writeTimeoutStr)
		return 1
	}
	shutdownTimeout, err := time.ParseDuration(options.shutdownTimeoutStr)
	if err != nil {
		log.Error(err, "invalid shutdown-timeout", "value", options.shutdownTimeoutStr)
		return 1
	}
	resumeLeaseDuration, err := time.ParseDuration(options.resumeLeaseDurationStr)
	if err != nil {
		log.Error(err, "invalid resume-lease-duration", "value", options.resumeLeaseDurationStr)
		return 1
	}

	// Use hostname as default holder identity if not specified
	leaseHolderIdentity := options.leaseHolderIdentity
	if leaseHolderIdentity == "" {
		hostname, hostnameErr := os.Hostname()
		if hostnameErr != nil {
			leaseHolderIdentity = config.APIServerDefaultLeaseHolderIdentity
		} else {
			leaseHolderIdentity = fmt.Sprintf("%s-%s", config.APIServerDefaultLeaseHolderIdentity, hostname)
		}
	}

	authToken, err := loadBearerToken(options.authEnabled, options.authTokenFile, "XTRINODE_API_SERVER_AUTH_TOKEN")
	if err != nil {
		log.Error(err, "invalid API server auth configuration")
		return 1
	}
	resumeAuthToken, err := loadOptionalBearerToken(options.authEnabled, options.resumeAuthTokenFile, "XTRINODE_API_SERVER_RESUME_AUTH_TOKEN")
	if err != nil {
		log.Error(err, "invalid API server resume auth configuration")
		return 1
	}
	if validateErr := validateAuthTokenConfiguration(options.authEnabled, authToken, resumeAuthToken); validateErr != nil {
		log.Error(validateErr, "invalid API server auth configuration")
		return 1
	}

	log.Info("Starting XTrinode API Server",
		"port", options.apiPort,
		"apiPath", options.apiPath,
		"healthPath", options.healthPath,
		"metricsPath", options.metricsPath,
		"readTimeout", readTimeout,
		"writeTimeout", writeTimeout,
		"resumeLeaseDuration", resumeLeaseDuration,
		"leaseNamespace", options.leaseNamespace,
		"leaseHolderIdentity", leaseHolderIdentity,
		"authEnabled", options.authEnabled,
		"resumeAuthEnabled", resumeAuthToken != "",
		"corsAllowedOrigins", parseCSV(options.corsAllowedOrigins),
		"version", version,
		"commit", commit,
		"buildDate", buildDate)

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

	// Create API server config
	serverConfig := apiserver.ServerConfig{
		Port:                 options.apiPort,
		APIPath:              options.apiPath,
		HealthPath:           options.healthPath,
		MetricsPath:          options.metricsPath,
		ReadTimeout:          readTimeout,
		WriteTimeout:         writeTimeout,
		ShutdownTimeout:      shutdownTimeout,
		ResumeLeaseDuration:  resumeLeaseDuration,
		SuspendLeaseDuration: config.APIServerSuspendLeaseDuration,
		RetryAfterSeconds:    config.APIServerRetryAfterSeconds,
		RequestTimeout:       config.APIServerRequestTimeout,
		LeaseNamespace:       options.leaseNamespace,
		LeaseHolderIdentity:  leaseHolderIdentity,
		AuthEnabled:          options.authEnabled,
		AuthToken:            authToken,
		ResumeAuthToken:      resumeAuthToken,
		CORSAllowedOrigins:   parseCSV(options.corsAllowedOrigins),
	}

	// Create API server
	apiServer := apiserver.NewServer(cli, log, &serverConfig)

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start API server in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- apiServer.Start(ctx)
	}()

	log.Info("API server started successfully", "port", options.apiPort)

	// Wait for signal or error
	select {
	case <-sigChan:
		log.Info("Received shutdown signal, shutting down gracefully")
		cancel()
		// Wait for Start() to complete its graceful shutdown (respects ShutdownTimeout internally)
		select {
		case err := <-errChan:
			if err != nil {
				log.Error(err, "API server error during shutdown")
			}
		case <-time.After(shutdownTimeout + 2*time.Second):
			log.Info("Shutdown timeout reached, forcing exit")
		}
	case err := <-errChan:
		if err != nil {
			log.Error(err, "API server error")
			cancel()
			return 1
		}
	}

	log.Info("API server stopped")
	return 0
}

func loadBearerToken(enabled bool, tokenFile, envName string) (string, error) {
	if !enabled {
		return "", nil
	}
	token := strings.TrimSpace(os.Getenv(envName))
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("failed to read auth token file: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		return "", fmt.Errorf("auth is enabled but no bearer token was provided")
	}
	return token, nil
}

func loadOptionalBearerToken(enabled bool, tokenFile, envName string) (string, error) {
	if !enabled {
		return "", nil
	}
	token := strings.TrimSpace(os.Getenv(envName))
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("failed to read optional auth token file: %w", err)
		}
		token = strings.TrimSpace(string(data))
		if token == "" {
			return "", fmt.Errorf("optional auth token file %q is empty", tokenFile)
		}
	}
	return token, nil
}

func validateAuthTokenConfiguration(enabled bool, adminToken, resumeToken string) error {
	if !enabled || resumeToken == "" {
		return nil
	}
	if adminToken == resumeToken {
		return fmt.Errorf("resume auth token must differ from admin auth token")
	}
	return nil
}

func parseCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
