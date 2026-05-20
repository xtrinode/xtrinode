package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/controllers"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	// Version information (set via ldflags during build)
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(analyticsv1.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
}

func main() {
	os.Exit(run())
}

type operatorOptions struct {
	enableLeaderElection           bool
	showVersion                    bool
	maxConcurrentReconciles        int
	maxConcurrentReconcilesCatalog int
	gatewayDrainDuration           time.Duration
	gatewayDrainRequeueInterval    time.Duration
	webhookEnabled                 bool
	webhookPort                    int
	webhookCertDir                 string
}

func defaultOperatorOptions() operatorOptions {
	return operatorOptions{
		enableLeaderElection:           config.LeaderElectionEnabled,
		maxConcurrentReconciles:        config.MaxConcurrentReconciles,
		maxConcurrentReconcilesCatalog: config.MaxConcurrentReconcilesCatalog,
		gatewayDrainDuration:           config.GatewayDrainDuration,
		gatewayDrainRequeueInterval:    config.GatewayDrainRequeueInterval,
		webhookEnabled:                 true,
		webhookPort:                    webhook.DefaultPort,
	}
}

func parseOperatorOptions(args []string, output io.Writer) (operatorOptions, zap.Options, error) {
	options := defaultOperatorOptions()
	zapOptions := zap.Options{Development: true}
	fs := flag.NewFlagSet("xtrinode-operator", flag.ContinueOnError)
	if output != nil {
		fs.SetOutput(output)
	}

	fs.BoolVar(&options.enableLeaderElection, "leader-elect", options.enableLeaderElection,
		"Enable leader election for controller manager.")
	fs.IntVar(&options.maxConcurrentReconciles, "max-concurrent-reconciles", options.maxConcurrentReconciles,
		"Maximum number of concurrent XTrinode reconciliations.")
	fs.IntVar(&options.maxConcurrentReconcilesCatalog, "max-concurrent-reconciles-catalog", options.maxConcurrentReconcilesCatalog,
		"Maximum number of concurrent XTrinodeCatalog reconciliations.")
	fs.DurationVar(&options.gatewayDrainDuration, "gateway-drain-duration", options.gatewayDrainDuration,
		"Maximum gateway route drain window before elapsed-time fallback is allowed.")
	fs.DurationVar(&options.gatewayDrainRequeueInterval, "gateway-drain-requeue-interval", options.gatewayDrainRequeueInterval,
		"Interval for query-aware gateway drain rechecks.")
	fs.BoolVar(&options.webhookEnabled, "webhook-enabled", options.webhookEnabled,
		"Enable admission webhooks for XTrinode resources.")
	fs.IntVar(&options.webhookPort, "webhook-port", options.webhookPort,
		"Port for the admission webhook HTTPS server.")
	fs.StringVar(&options.webhookCertDir, "webhook-cert-dir", "",
		"Directory containing admission webhook TLS certificate and key.")
	fs.BoolVar(&options.showVersion, "version", false, "Show version information and exit.")

	zapOptions.BindFlags(fs)
	if err := fs.Parse(args); err != nil {
		return options, zapOptions, err
	}
	return options, zapOptions, nil
}

func operatorNamespaceFromEnv() string {
	operatorNamespace := os.Getenv("POD_NAMESPACE")
	if operatorNamespace == "" {
		operatorNamespace = config.OperatorDefaultNamespace
	}
	return operatorNamespace
}

func buildManagerOptions(options operatorOptions, operatorNamespace string) ctrl.Options {
	managerOptions := ctrl.Options{
		Scheme:                        scheme,
		LeaderElection:                options.enableLeaderElection,
		LeaderElectionID:              config.OperatorLeaderElectionID,
		LeaderElectionNamespace:       operatorNamespace,
		LeaderElectionReleaseOnCancel: config.LeaderElectionReleaseOnCancel,
		HealthProbeBindAddress:        config.HealthProbeBindAddress,
	}
	if options.webhookEnabled {
		managerOptions.WebhookServer = webhook.NewServer(webhook.Options{
			Port:    options.webhookPort,
			CertDir: options.webhookCertDir,
		})
	}
	return managerOptions
}

func run() int {
	options, zapOptions, err := parseOperatorOptions(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if options.showVersion {
		fmt.Printf("xtrinode-operator version %s (commit: %s, built: %s)\n", version, commit, buildDate)
		return 0
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))

	// Configure REST client with higher QPS/burst for concurrent reconciles
	cfg := ctrl.GetConfigOrDie()
	cfg.QPS = config.OperatorRESTConfigQPS
	cfg.Burst = config.OperatorRESTConfigBurst

	// Get operator namespace from environment or use default
	operatorNamespace := operatorNamespaceFromEnv()

	managerOptions := buildManagerOptions(options, operatorNamespace)
	mgr, err := ctrl.NewManager(cfg, managerOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return 1
	}

	// Create all service dependencies (injected)
	nodePoolAdapter := controllers.NewNodePoolAdapter(mgr.GetClient(), setupLog)
	gatewayService := controllers.NewGatewayServiceWithDrainDuration(mgr.GetClient(), options.gatewayDrainDuration)
	kedaService := controllers.NewKEDAService(mgr.GetClient(), mgr.GetScheme())
	catalogService := controllers.NewCatalogService(mgr.GetClient())
	trinoResourcesService := controllers.NewTrinoResourcesService(mgr.GetClient(), mgr.GetScheme(), version)
	autosuspendService := controllers.NewAutosuspendService(mgr.GetClient())
	gracefulShutdownService := controllers.NewGracefulShutdownService(mgr.GetClient())

	// Create event recorder with configurable settings
	eventConfig := events.DefaultConfig().WithComponentName("xtrinode-operator")
	eventRecorder := events.NewRecorder(mgr.GetEventRecorderFor(eventConfig.ComponentName), eventConfig)

	// Create reconciler with all injected dependencies
	reconciler := &controllers.XTrinodeReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		EventRecorder:           eventRecorder,
		NodePoolAdapter:         nodePoolAdapter,
		GatewayService:          gatewayService,
		KEDAService:             kedaService,
		CatalogService:          catalogService,
		TrinoResourcesService:   trinoResourcesService,
		AutosuspendService:      autosuspendService,
		GracefulShutdownService: gracefulShutdownService,
		OperatorVersion:         version,
		DrainDuration:           options.gatewayDrainDuration,
		DrainRequeueInterval:    options.gatewayDrainRequeueInterval,
	}

	setupLog.Info("Controller initialized",
		"leaderElection", options.enableLeaderElection,
		"leaderElectionNamespace", operatorNamespace,
		"maxConcurrentReconciles", options.maxConcurrentReconciles,
		"maxConcurrentReconcilesCatalog", options.maxConcurrentReconcilesCatalog,
		"gatewayDrainDuration", options.gatewayDrainDuration,
		"gatewayDrainRequeueInterval", options.gatewayDrainRequeueInterval,
		"webhookEnabled", options.webhookEnabled,
		"webhookPort", options.webhookPort,
		"webhookCertDir", options.webhookCertDir,
		"qps", cfg.QPS,
		"burst", cfg.Burst)

	if err = reconciler.SetupWithManager(mgr, options.maxConcurrentReconciles); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "XTrinode")
		return 1
	}

	// Create XTrinodeCatalog reconciler with event recorder
	catalogEventConfig := events.DefaultConfig().WithComponentName("xtrinode-catalog-controller")
	catalogEventRecorder := events.NewRecorder(mgr.GetEventRecorderFor(catalogEventConfig.ComponentName), catalogEventConfig)
	catalogReconciler := &controllers.XTrinodeCatalogReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		EventRecorder: catalogEventRecorder,
	}
	if err = catalogReconciler.SetupWithManager(mgr, options.maxConcurrentReconcilesCatalog); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "XTrinodeCatalog")
		return 1
	}

	if options.webhookEnabled {
		if err = (&analyticsv1.XTrinode{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "XTrinode")
			return 1
		}
		if err = (&analyticsv1.XTrinodeCatalog{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "XTrinodeCatalog")
			return 1
		}
	} else {
		setupLog.Info("admission webhooks disabled")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return 1
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return 1
	}

	setupLog.Info("starting manager", "version", version, "commit", commit, "buildDate", buildDate)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return 1
	}
	return 0
}
