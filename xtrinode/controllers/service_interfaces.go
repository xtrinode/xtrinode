package controllers

import (
	"context"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/trino/resources"
)

// GatewayServiceInterface defines the interface for gateway route management
type GatewayServiceInterface interface {
	RegisterRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error
	DrainRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error
	DeregisterRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error
}

// KEDAServiceInterface defines the interface for KEDA ScaledObject management
type KEDAServiceInterface interface {
	EnsureScaledObject(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error
	DeleteScaledObject(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error
	DisableScaledObject(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error
	EnableScaledObjectWithWakeMinWorkers(ctx context.Context, xtrinode *analyticsv1.XTrinode, wakeMinWorkers int32, log logr.Logger) error
}

// CatalogServiceInterface defines the interface for catalog discovery and validation
type CatalogServiceInterface interface {
	GetEffectiveCatalogs(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) ([]string, error)
	ValidateCatalogConfigMaps(ctx context.Context, xtrinode *analyticsv1.XTrinode, catalogs []string, log logr.Logger) error
}

// TrinoResourcesServiceInterface defines the interface for Trino resource management
type TrinoResourcesServiceInterface interface {
	BuildTrinoResourceSet(ctx context.Context, xtrinode *analyticsv1.XTrinode, catalogs []string, operatorVersion string) (*resources.TrinoResourceSet, error)
	ApplyTrinoResources(ctx context.Context, xtrinode *analyticsv1.XTrinode, resourceSet *resources.TrinoResourceSet) error
	DeleteTrinoResources(ctx context.Context, resourceSet *resources.TrinoResourceSet) error
	GetXTrinodeRevision(xtrinode *analyticsv1.XTrinode, operatorVersion string, catalogs []string) string
}

// AutosuspendServiceInterface defines the interface for auto-suspend functionality
type AutosuspendServiceInterface interface {
	AutoSuspendIfNeeded(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) (bool, error)
}

// GracefulShutdownServiceInterface defines the interface for graceful shutdown checks
type GracefulShutdownServiceInterface interface {
	CheckQueriesBeforeScaleDown(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) (bool, error)
	WaitForPodTermination(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error
}
