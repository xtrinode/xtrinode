package controllers

import (
	"context"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// NodePoolAdapterInterface defines the interface for node pool provisioning adapters
// This allows for dependency injection and testing with mock implementations
type NodePoolAdapterInterface interface {
	// EnsureNodePool ensures a node pool exists for the XTrinode
	// Supports Azure CAPZ (MachinePool), AWS CAPA (MachineDeployment), GCP CAPG (MachineDeployment)
	EnsureNodePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error

	// DeleteNodePool deletes the node pool for the XTrinode
	DeleteNodePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error

	// RetainNodePool removes XTrinode owner references so retained node pools
	// survive XTrinode garbage collection.
	RetainNodePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error

	// ScaleNodePoolMinNodes scales the node pool minNodes to the specified value
	// This updates the MachinePool/MachineDeployment replicas and Cluster Autoscaler annotations
	ScaleNodePoolMinNodes(ctx context.Context, xtrinode *analyticsv1.XTrinode, minNodes int32) error
}
