package sizing

// Package sizing provides size presets for xtrinode resources.
//
// This package contains resource presets (CPU, memory, max workers) for different
// xtrinode sizes (xs, s, m, l, xl). These presets are used by trino/resources
// to build Kubernetes resources directly.

// SizePreset defines resource presets for each xtrinode size
type SizePreset struct {
	WorkerCPUReq      string
	WorkerMemReq      string
	WorkerCPULim      string
	WorkerMemLim      string
	MaxWorkers        int32
	CoordinatorCPUReq string
	CoordinatorMemReq string
	CoordinatorCPULim string
	CoordinatorMemLim string
	// Recommended machine types per cloud provider (user can override in spec.nodePool)
	RecommendedMachineTypes MachineTypeRecommendations
}

// MachineTypeRecommendations suggests appropriate machine types per cloud provider
// These are recommendations based on the worker resource requirements
// Users can override by explicitly setting spec.nodePool.{azure|aws|gcp}.{vmSize|instanceType|machineType}
type MachineTypeRecommendations struct {
	Azure string // Azure VM size (e.g., "Standard_D8as_v5")
	AWS   string // AWS instance type (e.g., "m5.2xlarge")
	GCP   string // GCP machine type (e.g., "n1-standard-8")
}

// Presets maps size names to resource presets
var Presets = map[string]SizePreset{
	"xs": {
		WorkerCPUReq:      "1",
		WorkerMemReq:      "4Gi",
		WorkerCPULim:      "2",
		WorkerMemLim:      "8Gi",
		MaxWorkers:        5,
		CoordinatorCPUReq: "1",
		CoordinatorMemReq: "4Gi",
		CoordinatorCPULim: "2",
		CoordinatorMemLim: "8Gi",
		RecommendedMachineTypes: MachineTypeRecommendations{
			Azure: "Standard_D2as_v5", // 2 vCPU, 8 GiB RAM
			AWS:   "m5.large",         // 2 vCPU, 8 GiB RAM
			GCP:   "n1-standard-2",    // 2 vCPU, 7.5 GiB RAM
		},
	},
	"s": {
		WorkerCPUReq:      "2",
		WorkerMemReq:      "8Gi",
		WorkerCPULim:      "8",
		WorkerMemLim:      "32Gi",
		MaxWorkers:        24,
		CoordinatorCPUReq: "1",
		CoordinatorMemReq: "4Gi",
		CoordinatorCPULim: "2",
		CoordinatorMemLim: "8Gi",
		RecommendedMachineTypes: MachineTypeRecommendations{
			Azure: "Standard_D8as_v5", // 8 vCPU, 32 GiB RAM
			AWS:   "m5.2xlarge",       // 8 vCPU, 32 GiB RAM
			GCP:   "n1-standard-8",    // 8 vCPU, 30 GiB RAM
		},
	},
	"m": {
		WorkerCPUReq:      "4",
		WorkerMemReq:      "16Gi",
		WorkerCPULim:      "16",
		WorkerMemLim:      "64Gi",
		MaxWorkers:        50,
		CoordinatorCPUReq: "1",
		CoordinatorMemReq: "4Gi",
		CoordinatorCPULim: "2",
		CoordinatorMemLim: "8Gi",
		RecommendedMachineTypes: MachineTypeRecommendations{
			Azure: "Standard_D16as_v5", // 16 vCPU, 64 GiB RAM
			AWS:   "m5.4xlarge",        // 16 vCPU, 64 GiB RAM
			GCP:   "n1-standard-16",    // 16 vCPU, 60 GiB RAM
		},
	},
	"l": {
		WorkerCPUReq:      "8",
		WorkerMemReq:      "32Gi",
		WorkerCPULim:      "32",
		WorkerMemLim:      "128Gi",
		MaxWorkers:        100,
		CoordinatorCPUReq: "1",
		CoordinatorMemReq: "4Gi",
		CoordinatorCPULim: "2",
		CoordinatorMemLim: "8Gi",
		RecommendedMachineTypes: MachineTypeRecommendations{
			Azure: "Standard_D32as_v5", // 32 vCPU, 128 GiB RAM
			AWS:   "m5.8xlarge",        // 32 vCPU, 128 GiB RAM
			GCP:   "n1-standard-32",    // 32 vCPU, 120 GiB RAM
		},
	},
	"xl": {
		WorkerCPUReq:      "16",
		WorkerMemReq:      "64Gi",
		WorkerCPULim:      "64",
		WorkerMemLim:      "256Gi",
		MaxWorkers:        200,
		CoordinatorCPUReq: "1",
		CoordinatorMemReq: "4Gi",
		CoordinatorCPULim: "2",
		CoordinatorMemLim: "8Gi",
		RecommendedMachineTypes: MachineTypeRecommendations{
			Azure: "Standard_D64as_v5", // 64 vCPU, 256 GiB RAM
			AWS:   "m5.16xlarge",       // 64 vCPU, 256 GiB RAM
			GCP:   "n1-standard-64",    // 64 vCPU, 240 GiB RAM
		},
	},
}

// GetPreset returns the size preset for a given size name
func GetPreset(size string) (SizePreset, bool) {
	preset, ok := Presets[size]
	return preset, ok
}

// GetRecommendedMachineType returns the recommended machine type for a given size and provider
// Returns empty string if size or provider is invalid
func GetRecommendedMachineType(size, provider string) string {
	preset, ok := Presets[size]
	if !ok {
		return ""
	}

	switch provider {
	case "azure":
		return preset.RecommendedMachineTypes.Azure
	case "aws":
		return preset.RecommendedMachineTypes.AWS
	case "gcp":
		return preset.RecommendedMachineTypes.GCP
	default:
		return ""
	}
}
