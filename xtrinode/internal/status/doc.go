// Package status defines XTrinode lifecycle phases, status conditions, and phase invariants.
//
// The operator treats phases as controller-owned intent and observations. Phase helpers validate
// allowed transitions, while invariant helpers describe the Kubernetes resource shape expected for
// each phase: coordinator replicas, worker floor, KEDA ownership, gateway exposure, and optional
// node-pool floors.
package status
