package controllers

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/autosuspend"
	"github.com/xtrinode/xtrinode/internal/catalog"
	"github.com/xtrinode/xtrinode/internal/gracefulshutdown"
	"github.com/xtrinode/xtrinode/internal/keda"
	"github.com/xtrinode/xtrinode/internal/trino/resources"
	"github.com/xtrinode/xtrinode/pkg/gateway"
)

// GatewayService wraps the gateway package functions
type GatewayService struct {
	client client.Client
}

// NewGatewayService creates a new GatewayService
func NewGatewayService(cli client.Client) GatewayServiceInterface {
	return &GatewayService{client: cli}
}

// RegisterRoute registers a gateway route
func (g *GatewayService) RegisterRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return gateway.RegisterRoute(ctx, g.client, xtrinode)
}

// DrainRoute sets a backend to DRAINING state for graceful removal
func (g *GatewayService) DrainRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return gateway.DrainRoute(ctx, g.client, xtrinode)
}

// DeregisterRoute deregisters a gateway route
func (g *GatewayService) DeregisterRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return gateway.DeregisterRoute(ctx, g.client, xtrinode)
}

// KEDAService wraps the keda package functions
type KEDAService struct {
	client client.Client
	scheme *runtime.Scheme
}

// NewKEDAService creates a new KEDAService
func NewKEDAService(cli client.Client, scheme *runtime.Scheme) KEDAServiceInterface {
	return &KEDAService{client: cli, scheme: scheme}
}

// EnsureScaledObject ensures a KEDA ScaledObject exists
func (k *KEDAService) EnsureScaledObject(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	return keda.EnsureScaledObject(ctx, k.client, k.scheme, xtrinode, log)
}

// DeleteScaledObject deletes a KEDA ScaledObject
func (k *KEDAService) DeleteScaledObject(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	return keda.DeleteScaledObject(ctx, k.client, xtrinode, log)
}

// DisableScaledObject disables a KEDA ScaledObject
func (k *KEDAService) DisableScaledObject(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	return keda.DisableScaledObject(ctx, k.client, xtrinode, log)
}

// EnableScaledObjectWithWakeMinWorkers enables KEDA scaling with wakeMinWorkers as min
func (k *KEDAService) EnableScaledObjectWithWakeMinWorkers(ctx context.Context, xtrinode *analyticsv1.XTrinode, wakeMinWorkers int32, log logr.Logger) error {
	return keda.EnableScaledObjectWithWakeMinWorkers(ctx, k.client, k.scheme, xtrinode, wakeMinWorkers, log)
}

// CatalogService wraps the catalog package functions
type CatalogService struct {
	client client.Client
}

// NewCatalogService creates a new CatalogService
func NewCatalogService(cli client.Client) CatalogServiceInterface {
	return &CatalogService{client: cli}
}

// GetEffectiveCatalogs gets effective catalogs for a XTrinode
func (c *CatalogService) GetEffectiveCatalogs(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) ([]string, error) {
	return catalog.GetEffectiveCatalogs(ctx, c.client, xtrinode, log)
}

// ValidateCatalogConfigMaps validates catalog ConfigMaps
func (c *CatalogService) ValidateCatalogConfigMaps(ctx context.Context, xtrinode *analyticsv1.XTrinode, catalogs []string, log logr.Logger) error {
	return catalog.ValidateCatalogConfigMaps(ctx, c.client, xtrinode, catalogs, log)
}

// TrinoResourcesService wraps the resources package functions
type TrinoResourcesService struct {
	client          client.Client
	scheme          *runtime.Scheme
	operatorVersion string
}

// NewTrinoResourcesService creates a new TrinoResourcesService
func NewTrinoResourcesService(cli client.Client, scheme *runtime.Scheme, operatorVersion string) TrinoResourcesServiceInterface {
	return &TrinoResourcesService{
		client:          cli,
		scheme:          scheme,
		operatorVersion: operatorVersion,
	}
}

// BuildTrinoResourceSet builds a Trino resource set
func (t *TrinoResourcesService) BuildTrinoResourceSet(ctx context.Context, xtrinode *analyticsv1.XTrinode, catalogs []string, operatorVersion string) (*resources.TrinoResourceSet, error) {
	return resources.BuildTrinoResourceSet(ctx, t.client, xtrinode, catalogs, operatorVersion)
}

// ApplyTrinoResources applies Trino resources
func (t *TrinoResourcesService) ApplyTrinoResources(ctx context.Context, xtrinode *analyticsv1.XTrinode, resourceSet *resources.TrinoResourceSet) error {
	return resources.ApplyTrinoResources(ctx, t.client, t.scheme, xtrinode, resourceSet)
}

// DeleteTrinoResources deletes Trino resources
func (t *TrinoResourcesService) DeleteTrinoResources(ctx context.Context, resourceSet *resources.TrinoResourceSet) error {
	return resources.DeleteTrinoResources(ctx, t.client, resourceSet)
}

// GetXTrinodeRevision gets the XTrinode revision
func (t *TrinoResourcesService) GetXTrinodeRevision(xtrinode *analyticsv1.XTrinode, operatorVersion string, catalogs []string) string {
	return resources.GetXTrinodeRevision(xtrinode, operatorVersion, catalogs)
}

// AutosuspendService wraps the autosuspend package functions
type AutosuspendService struct {
	client client.Client
}

// NewAutosuspendService creates a new AutosuspendService
func NewAutosuspendService(cli client.Client) AutosuspendServiceInterface {
	return &AutosuspendService{client: cli}
}

// AutoSuspendIfNeeded checks and auto-suspends if needed
func (a *AutosuspendService) AutoSuspendIfNeeded(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) (bool, error) {
	return autosuspend.AutoSuspendIfNeeded(ctx, a.client, xtrinode, log)
}

// GracefulShutdownService wraps the gracefulshutdown package functions
type GracefulShutdownService struct {
	client client.Client
}

// NewGracefulShutdownService creates a new GracefulShutdownService
func NewGracefulShutdownService(cli client.Client) GracefulShutdownServiceInterface {
	return &GracefulShutdownService{client: cli}
}

// CheckQueriesBeforeScaleDown checks if it's safe to scale down
func (g *GracefulShutdownService) CheckQueriesBeforeScaleDown(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) (bool, error) {
	return gracefulshutdown.CheckQueriesBeforeScaleDown(ctx, g.client, xtrinode, log)
}

// WaitForPodTermination waits for pods to finish terminating gracefully
func (g *GracefulShutdownService) WaitForPodTermination(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	return gracefulshutdown.WaitForPodTermination(ctx, g.client, xtrinode, log)
}
