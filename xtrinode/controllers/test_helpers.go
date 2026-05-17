package controllers

import (
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/events"
)

func init() {
	//nolint:errcheck // test setup; panic on scheme registration failure is acceptable
	_ = clientgoscheme.AddToScheme(runtime.NewScheme())
	//nolint:errcheck // test setup; panic on scheme registration failure is acceptable
	_ = analyticsv1.AddToScheme(runtime.NewScheme())
	//nolint:errcheck // test setup; panic on scheme registration failure is acceptable
	_ = kedav1alpha1.AddToScheme(runtime.NewScheme())
}

// newTestScheme creates a new runtime.Scheme with all required types for testing
func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	//nolint:errcheck // test setup; panic on scheme registration failure is acceptable
	_ = clientgoscheme.AddToScheme(scheme)
	//nolint:errcheck // test setup; panic on scheme registration failure is acceptable
	_ = analyticsv1.AddToScheme(scheme)
	//nolint:errcheck // test setup; panic on scheme registration failure is acceptable
	_ = kedav1alpha1.AddToScheme(scheme)
	return scheme
}

// newTestSchemeAnalyticsOnly creates a scheme with only analytics types (for simpler tests)
func newTestSchemeAnalyticsOnly() *runtime.Scheme {
	scheme := runtime.NewScheme()
	//nolint:errcheck // test setup; panic on scheme registration failure is acceptable
	_ = analyticsv1.AddToScheme(scheme)
	return scheme
}

// newTestClient creates a fake client with the test scheme
func newTestClient(scheme *runtime.Scheme, objects ...client.Object) client.Client {
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range objects {
		builder = builder.WithObjects(obj)
	}
	return builder.Build()
}

// newTestLogger creates a test logger
func newTestLogger() logr.Logger {
	return zap.New(zap.UseDevMode(true))
}

// int32Ptr is a helper to create int32 pointers in tests
func int32Ptr(i int32) *int32 {
	return &i
}

// newTestReconciler creates a XTrinodeReconciler with all dependencies initialized for testing
func newTestReconciler(cli client.Client, scheme *runtime.Scheme) *XTrinodeReconciler {
	logger := newTestLogger()
	fakeRecorder := record.NewFakeRecorder(10)
	eventRecorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())
	return &XTrinodeReconciler{
		Client:                  cli,
		Scheme:                  scheme,
		EventRecorder:           eventRecorder,
		NodePoolAdapter:         NewNodePoolAdapter(cli, logger),
		GatewayService:          NewGatewayService(cli),
		KEDAService:             NewKEDAService(cli, scheme),
		CatalogService:          NewCatalogService(cli),
		TrinoResourcesService:   NewTrinoResourcesService(cli, scheme, "test-version"),
		AutosuspendService:      NewAutosuspendService(cli),
		GracefulShutdownService: NewGracefulShutdownService(cli),
		OperatorVersion:         "test-version",
	}
}

// newTestCatalogReconciler creates a XTrinodeCatalogReconciler with EventRecorder initialized for testing
func newTestCatalogReconciler(cli client.Client, scheme *runtime.Scheme) *XTrinodeCatalogReconciler {
	fakeRecorder := record.NewFakeRecorder(10)
	eventRecorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())
	return &XTrinodeCatalogReconciler{
		Client:        cli,
		Scheme:        scheme,
		EventRecorder: eventRecorder,
	}
}
