package v1

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// valuesOverlayFromMap converts a map[string]interface{} to *apiextensionsv1.JSON for testing
func valuesOverlayFromMap(m map[string]interface{}) *apiextensionsv1.JSON {
	if m == nil {
		return nil
	}
	data, _ := json.Marshal(m)
	return &apiextensionsv1.JSON{Raw: data}
}

func TestXTrinode_Default(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *XTrinode
		expected func(*XTrinode)
	}{
		{
			name: "sets default minWorkers to 0",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size: "s",
				},
			},
			expected: func(tr *XTrinode) {
				assert.NotNil(t, tr.Spec.MinWorkers)
				assert.Equal(t, int32(0), *tr.Spec.MinWorkers)
			},
		},
		{
			name: "sets default nodePool name",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
						Azure: &AzureNodePoolSpec{
							VMSize: "Standard_D8as_v5",
						},
					},
				},
			},
			expected: func(tr *XTrinode) {
				assert.Equal(t, "test-dummy-pool", tr.Spec.NodePool.Name)
			},
		},
		{
			name: "sets default nodePool minNodes to 0",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
						Azure: &AzureNodePoolSpec{
							VMSize: "Standard_D8as_v5",
						},
					},
				},
			},
			expected: func(tr *XTrinode) {
				assert.NotNil(t, tr.Spec.NodePool.MinNodes)
				assert.Equal(t, int32(0), *tr.Spec.NodePool.MinNodes)
			},
		},
		{
			name: "sets default nodePool maxNodes based on maxWorkers",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size:       "s",
					MaxWorkers: int32Ptr(24),
					NodePool: &NodePoolSpec{
						Provider: "azure",
						Azure: &AzureNodePoolSpec{
							VMSize: "Standard_D8as_v5",
						},
					},
				},
			},
			expected: func(tr *XTrinode) {
				assert.NotNil(t, tr.Spec.NodePool.MaxNodes)
				assert.Equal(t, int32(24), *tr.Spec.NodePool.MaxNodes)
			},
		},
		{
			name: "sets default nodePool osDiskGB to 128",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
						Azure: &AzureNodePoolSpec{
							VMSize: "Standard_D8as_v5",
						},
					},
				},
			},
			expected: func(tr *XTrinode) {
				assert.NotNil(t, tr.Spec.NodePool.OSDiskGB)
				assert.Equal(t, int32(128), *tr.Spec.NodePool.OSDiskGB)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.xtrinode.Default()
			tt.expected(tt.xtrinode)
		})
	}
}

func TestXTrinode_ValidateCreate(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *XTrinode
		wantErr  bool
	}{
		{
			name: "valid XTrinode",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid size",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "invalid",
				},
			},
			wantErr: true,
		},
		{
			name: "missing size",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{},
			},
			wantErr: true,
		},
		{
			name: "maxWorkers too low",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size:       "s",
					MaxWorkers: int32Ptr(0),
				},
			},
			wantErr: true,
		},
		{
			name: "maxWorkers too high",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size:       "s",
					MaxWorkers: int32Ptr(501),
				},
			},
			wantErr: true,
		},
		{
			name: "minWorkers greater than maxWorkers",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size:       "s",
					MinWorkers: int32Ptr(10),
					MaxWorkers: int32Ptr(5),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid nodePool provider",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "invalid",
						Azure: &AzureNodePoolSpec{
							VMSize: "Standard_D8as_v5",
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "missing nodePool vmSize",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "nodePool minNodes greater than maxNodes",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
						Azure: &AzureNodePoolSpec{
							VMSize: "Standard_D8as_v5",
						},
						MinNodes: int32Ptr(10),
						MaxNodes: int32Ptr(5),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid routing - no header, hostname, or default",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size:    "s",
					Routing: &RoutingSpec{
						// Empty routing spec
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid routing with header",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
					Routing: &RoutingSpec{
						Header: "X-Trino-XTrinode=dummy",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid routing with hostname",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
					Routing: &RoutingSpec{
						Hostname: "dummy.trino.company",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid routing with default",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "s",
					Routing: &RoutingSpec{
						Default: true,
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestXTrinode_ValidateUpdate(t *testing.T) {
	oldXTrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dummy",
		},
		Spec: XTrinodeSpec{
			Size: "s",
		},
	}

	tests := []struct {
		name    string
		old     *XTrinode
		new     *XTrinode
		wantErr bool
	}{
		{
			name: "valid update",
			old:  oldXTrinode,
			new: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "m", // Size change allowed
				},
			},
			wantErr: false,
		},
		{
			name: "invalid new spec",
			old:  oldXTrinode,
			new: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dummy",
				},
				Spec: XTrinodeSpec{
					Size: "invalid",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.new.ValidateUpdate(tt.old)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestXTrinode_ValidateDelete(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dummy",
		},
		Spec: XTrinodeSpec{
			Size: "s",
		},
	}

	warnings, err := xtrinode.ValidateDelete()
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}

func TestValidateNodePool(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *NodePoolSpec
		wantErr  bool
	}{
		{
			name: "valid Azure nodePool",
			nodePool: &NodePoolSpec{
				Provider: "azure",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
				MinNodes: int32Ptr(0),
				MaxNodes: int32Ptr(10),
			},
			wantErr: false,
		},
		{
			name: "valid AWS nodePool",
			nodePool: &NodePoolSpec{
				Provider: "aws",
				AWS: &AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
				MinNodes: int32Ptr(0),
				MaxNodes: int32Ptr(10),
			},
			wantErr: false,
		},
		{
			name: "valid GCP nodePool",
			nodePool: &NodePoolSpec{
				Provider: "gcp",
				GCP: &GCPNodePoolSpec{
					MachineType: "n1-standard-4",
				},
				MinNodes: int32Ptr(0),
				MaxNodes: int32Ptr(10),
			},
			wantErr: false,
		},
		{
			name: "missing provider",
			nodePool: &NodePoolSpec{
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid provider",
			nodePool: &NodePoolSpec{
				Provider: "invalid",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
			},
			wantErr: true,
		},
		{
			name: "missing vmSize",
			nodePool: &NodePoolSpec{
				Provider: "azure",
			},
			wantErr: true,
		},
		{
			name: "minNodes negative",
			nodePool: &NodePoolSpec{
				Provider: "azure",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
				MinNodes: int32Ptr(-1),
			},
			wantErr: true,
		},
		{
			name: "minNodes greater than maxNodes",
			nodePool: &NodePoolSpec{
				Provider: "azure",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
				MinNodes: int32Ptr(10),
				MaxNodes: int32Ptr(5),
			},
			wantErr: true,
		},
		{
			name: "osDiskGB too small",
			nodePool: &NodePoolSpec{
				Provider: "azure",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
				OSDiskGB: int32Ptr(29),
			},
			wantErr: true,
		},
		{
			name: "osDiskGB too large",
			nodePool: &NodePoolSpec{
				Provider: "azure",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
				OSDiskGB: int32Ptr(2049),
			},
			wantErr: true,
		},
		{
			name: "maxNodes too small",
			nodePool: &NodePoolSpec{
				Provider: "azure",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
				MaxNodes: int32Ptr(0),
			},
			wantErr: true,
		},
		{
			name: "maxNodes too large",
			nodePool: &NodePoolSpec{
				Provider: "azure",
				Azure: &AzureNodePoolSpec{
					VMSize: "Standard_D8as_v5",
				},
				MaxNodes: int32Ptr(1001),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				Spec: XTrinodeSpec{
					Size:     "s",
					NodePool: tt.nodePool,
				},
			}
			errs := xtrinode.validateNodePool(field.NewPath("spec.nodePool"))
			if tt.wantErr {
				assert.NotEmpty(t, errs)
			} else {
				assert.Empty(t, errs)
			}
		})
	}
}

func TestXTrinode_ValidateUpdate_InvalidOldObject(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dummy",
		},
		Spec: XTrinodeSpec{
			Size: "s",
		},
	}

	// Test with invalid old object type
	_, err := xtrinode.ValidateUpdate(&XTrinodeList{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected old object to be of type XTrinode")
}

func TestXTrinode_DeepCopy(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy",
			Namespace: "team-a",
		},
		Spec: XTrinodeSpec{
			Size:       "s",
			MaxWorkers: int32Ptr(10),
			MinWorkers: int32Ptr(0),
		},
		Status: XTrinodeStatus{
			Phase:          "Ready",
			CoordinatorURL: "http://coordinator:8080",
			Workers:        5,
		},
	}

	// Test DeepCopy
	copied := xtrinode.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, xtrinode.Name, copied.Name)
	assert.Equal(t, xtrinode.Spec.Size, copied.Spec.Size)
	assert.Equal(t, *xtrinode.Spec.MaxWorkers, *copied.Spec.MaxWorkers)
	assert.Equal(t, xtrinode.Status.Phase, copied.Status.Phase)

	// Modify copy and verify original unchanged
	copied.Spec.Size = "m"
	copied.Status.Workers = 10
	assert.Equal(t, "s", xtrinode.Spec.Size)
	assert.Equal(t, int32(5), xtrinode.Status.Workers)
}

func TestXTrinode_DeepCopyInto(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy",
			Namespace: "team-a",
		},
		Spec: XTrinodeSpec{
			Size:       "s",
			MaxWorkers: int32Ptr(10),
			MinWorkers: int32Ptr(0),
		},
		Status: XTrinodeStatus{
			Phase: "Ready",
		},
	}

	out := &XTrinode{}
	xtrinode.DeepCopyInto(out)
	assert.Equal(t, xtrinode.Name, out.Name)
	assert.Equal(t, xtrinode.Spec.Size, out.Spec.Size)
	assert.Equal(t, *xtrinode.Spec.MaxWorkers, *out.Spec.MaxWorkers)
}

func TestXTrinodeList_DeepCopy(t *testing.T) {
	list := &XTrinodeList{
		Items: []XTrinode{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "test-1"},
				Spec:       XTrinodeSpec{Size: "s"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "test-2"},
				Spec:       XTrinodeSpec{Size: "m"},
			},
		},
	}

	copied := list.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(list.Items), len(copied.Items))
	assert.Equal(t, list.Items[0].Name, copied.Items[0].Name)
}

func TestXTrinodeList_DeepCopyInto(t *testing.T) {
	list := &XTrinodeList{
		Items: []XTrinode{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "test-1"},
				Spec:       XTrinodeSpec{Size: "s"},
			},
		},
	}

	out := &XTrinodeList{}
	list.DeepCopyInto(out)
	assert.Equal(t, len(list.Items), len(out.Items))
	assert.Equal(t, list.Items[0].Name, out.Items[0].Name)
}

func TestXTrinode_DeepCopy_Nil(t *testing.T) {
	var xtrinode *XTrinode
	copied := xtrinode.DeepCopy()
	assert.Nil(t, copied)
}

func TestXTrinodeList_DeepCopy_Nil(t *testing.T) {
	var list *XTrinodeList
	copied := list.DeepCopy()
	assert.Nil(t, copied)
}

func TestXTrinode_DeepCopyObject(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy",
			Namespace: "team-a",
		},
		Spec: XTrinodeSpec{
			Size: "s",
		},
	}

	obj := xtrinode.DeepCopyObject()
	assert.NotNil(t, obj)
	copied, ok := obj.(*XTrinode)
	assert.True(t, ok)
	assert.Equal(t, xtrinode.Name, copied.Name)
	assert.Equal(t, xtrinode.Spec.Size, copied.Spec.Size)
}

func TestXTrinodeList_DeepCopyObject(t *testing.T) {
	list := &XTrinodeList{
		Items: []XTrinode{
			{ObjectMeta: metav1.ObjectMeta{Name: "test-1"}},
		},
	}

	obj := list.DeepCopyObject()
	assert.NotNil(t, obj)
	copied, ok := obj.(*XTrinodeList)
	assert.True(t, ok)
	assert.Equal(t, len(list.Items), len(copied.Items))
}

func TestXTrinodeCatalog_DeepCopy(t *testing.T) {
	now := metav1.Now()
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "team-a",
		},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Postgres: &PostgresCatalogSpec{
					ConnectionURL: "jdbc:postgresql://localhost:5432/testdb",
					Properties: map[string]string{
						"connection-url": "jdbc:postgresql://localhost:5432/testdb",
					},
				},
			},
			Labels: map[string]string{
				"team": "data-eng",
			},
		},
		Status: XTrinodeCatalogStatus{
			Phase:       "Ready",
			LastUpdated: &now,
		},
	}

	// Test DeepCopy
	copied := catalog.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, catalog.Name, copied.Name)
	assert.Equal(t, catalog.Spec.Connector.Postgres.ConnectionURL, copied.Spec.Connector.Postgres.ConnectionURL)
	assert.Equal(t, catalog.Spec.Labels["team"], copied.Spec.Labels["team"])
	assert.Equal(t, catalog.Status.Phase, copied.Status.Phase)

	// Modify copy and verify original unchanged
	copied.Spec.Labels["team"] = "data-science"
	copied.Spec.Connector.Postgres.ConnectionURL = "jdbc:postgresql://remote-host:5432/testdb"
	copied.Status.Phase = "Error"
	assert.Equal(t, "data-eng", catalog.Spec.Labels["team"])
	assert.Equal(t, "jdbc:postgresql://localhost:5432/testdb", catalog.Spec.Connector.Postgres.ConnectionURL)
	assert.Equal(t, "Ready", catalog.Status.Phase)
}

func TestXTrinodeCatalog_DeepCopyInto(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "team-a",
		},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Hive: &HiveCatalogSpec{
					MetastoreURI: "thrift://localhost:9083",
					Properties: map[string]string{
						"hive.metastore.uri": "thrift://localhost:9083",
					},
				},
			},
			Labels: map[string]string{
				"team": "data-eng",
			},
		},
	}

	out := &XTrinodeCatalog{}
	catalog.DeepCopyInto(out)
	assert.Equal(t, catalog.Name, out.Name)
	assert.Equal(t, catalog.Spec.Connector.Hive.MetastoreURI, out.Spec.Connector.Hive.MetastoreURI)
	assert.Equal(t, catalog.Spec.Labels["team"], out.Spec.Labels["team"])
}

func TestXTrinodeCatalogList_DeepCopy(t *testing.T) {
	list := &XTrinodeCatalogList{
		Items: []XTrinodeCatalog{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "catalog-1"},
				Spec: XTrinodeCatalogSpec{
					Connector: XTrinodeCatalogConnector{
						Postgres: &PostgresCatalogSpec{
							ConnectionURL: "jdbc:postgresql://host1:5432/db",
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "catalog-2"},
				Spec: XTrinodeCatalogSpec{
					Connector: XTrinodeCatalogConnector{
						Hive: &HiveCatalogSpec{MetastoreURI: "thrift://host2:9083"},
					},
				},
			},
		},
	}

	copied := list.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(list.Items), len(copied.Items))
	assert.Equal(t, list.Items[0].Name, copied.Items[0].Name)
	assert.Equal(t, list.Items[1].Name, copied.Items[1].Name)
}

func TestXTrinodeCatalogList_DeepCopyInto(t *testing.T) {
	list := &XTrinodeCatalogList{
		Items: []XTrinodeCatalog{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "catalog-1"},
				Spec: XTrinodeCatalogSpec{
					Connector: XTrinodeCatalogConnector{
						Kafka: &KafkaCatalogSpec{
							KafkaNodes: []string{"kafka1:9092", "kafka2:9092"},
						},
					},
				},
			},
		},
	}

	out := &XTrinodeCatalogList{}
	list.DeepCopyInto(out)
	assert.Equal(t, len(list.Items), len(out.Items))
	assert.Equal(t, list.Items[0].Name, out.Items[0].Name)
	assert.Equal(t, len(list.Items[0].Spec.Connector.Kafka.KafkaNodes), len(out.Items[0].Spec.Connector.Kafka.KafkaNodes))
}

func TestXTrinodeCatalog_DeepCopy_Nil(t *testing.T) {
	var catalog *XTrinodeCatalog
	copied := catalog.DeepCopy()
	assert.Nil(t, copied)
}

func TestXTrinodeCatalogList_DeepCopy_Nil(t *testing.T) {
	var list *XTrinodeCatalogList
	copied := list.DeepCopy()
	assert.Nil(t, copied)
}

func TestXTrinodeCatalog_DeepCopyObject(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "team-a",
		},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Iceberg: &IcebergCatalogSpec{
					CatalogType:  "hive",
					WarehouseURI: "s3://bucket/catalog",
				},
			},
		},
	}

	obj := catalog.DeepCopyObject()
	assert.NotNil(t, obj)
	copied, ok := obj.(*XTrinodeCatalog)
	assert.True(t, ok)
	assert.Equal(t, catalog.Name, copied.Name)
	assert.Equal(t, catalog.Spec.Connector.Iceberg.WarehouseURI, copied.Spec.Connector.Iceberg.WarehouseURI)
}

func TestXTrinodeCatalogList_DeepCopyObject(t *testing.T) {
	list := &XTrinodeCatalogList{
		Items: []XTrinodeCatalog{
			{ObjectMeta: metav1.ObjectMeta{Name: "catalog-1"}},
		},
	}

	obj := list.DeepCopyObject()
	assert.NotNil(t, obj)
	copied, ok := obj.(*XTrinodeCatalogList)
	assert.True(t, ok)
	assert.Equal(t, len(list.Items), len(copied.Items))
}

func TestXTrinodeCatalogConnector_DeepCopy(t *testing.T) {
	connector := &XTrinodeCatalogConnector{
		Custom: &CustomCatalogSpec{
			ConnectorName: "custom-connector",
			Properties: map[string]string{
				"custom.property": "value",
			},
		},
	}

	copied := connector.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, connector.Custom.ConnectorName, copied.Custom.ConnectorName)
	assert.Equal(t, connector.Custom.Properties["custom.property"], copied.Custom.Properties["custom.property"])

	// Modify copy and verify original unchanged
	copied.Custom.Properties["custom.property"] = "modified"
	assert.Equal(t, "value", connector.Custom.Properties["custom.property"])
}

func TestXTrinodeSpec_DeepCopy_WithAllFields(t *testing.T) {
	spec := &XTrinodeSpec{
		Size:       "m",
		MaxWorkers: int32Ptr(20),
		MinWorkers: int32Ptr(2),
		NodePool: &NodePoolSpec{
			MinNodes: int32Ptr(1),
			MaxNodes: int32Ptr(10),
		},
		KEDA: &KEDASpec{
			Enabled: boolPtr(true),
		},
		ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
			"custom": "value",
		}),
		CustomConfigMaps: []string{"config1", "config2"},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, spec.Size, copied.Size)
	assert.Equal(t, *spec.MaxWorkers, *copied.MaxWorkers)
	assert.Equal(t, *spec.MinWorkers, *copied.MinWorkers)
	assert.Equal(t, *spec.NodePool.MinNodes, *copied.NodePool.MinNodes)
	assert.Equal(t, *spec.KEDA.Enabled, *copied.KEDA.Enabled)
	specOverlay := spec.GetValuesOverlayMap()
	copyOverlay := copied.GetValuesOverlayMap()
	assert.Equal(t, specOverlay["custom"], copyOverlay["custom"])
	assert.Equal(t, len(spec.CustomConfigMaps), len(copied.CustomConfigMaps))

	// Modify copy and verify original unchanged
	copied.Size = "l"
	*copied.MaxWorkers = 50
	copyOverlay = copied.GetValuesOverlayMap()
	copyOverlay["custom"] = "modified"
	copyData, _ := json.Marshal(copyOverlay)
	copied.ValuesOverlay = &apiextensionsv1.JSON{Raw: copyData}
	assert.Equal(t, "m", spec.Size)
	assert.Equal(t, int32(20), *spec.MaxWorkers)
	specOverlay = spec.GetValuesOverlayMap()
	assert.Equal(t, "value", specOverlay["custom"])
}

func TestXTrinodeStatus_DeepCopy_WithAllFields(t *testing.T) {
	now := metav1.Now()
	status := &XTrinodeStatus{
		Phase:              "Ready",
		CoordinatorURL:     "http://coordinator:8080",
		Workers:            10,
		LastActivity:       &now,
		CurrentRevision:    "abc12345",
		ObservedGeneration: 5,
		Conditions: []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: now,
				Reason:             "Reconciled",
				Message:            "XTrinode is ready",
			},
		},
	}

	copied := status.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, status.Phase, copied.Phase)
	assert.Equal(t, status.CoordinatorURL, copied.CoordinatorURL)
	assert.Equal(t, status.Workers, copied.Workers)
	assert.Equal(t, status.CurrentRevision, copied.CurrentRevision)
	assert.Equal(t, status.ObservedGeneration, copied.ObservedGeneration)
	assert.Equal(t, len(status.Conditions), len(copied.Conditions))
	assert.Equal(t, status.Conditions[0].Type, copied.Conditions[0].Type)

	// Modify copy and verify original unchanged
	copied.Phase = "Error"
	copied.Workers = 20
	copied.Conditions[0].Message = "Modified"
	assert.Equal(t, "Ready", status.Phase)
	assert.Equal(t, int32(10), status.Workers)
	assert.Equal(t, "XTrinode is ready", status.Conditions[0].Message)
}

func TestXTrinodeSpec_DeepCopy_WithAllFieldTypes(t *testing.T) {
	duration := metav1.Duration{Duration: 5 * time.Minute}
	spec := &XTrinodeSpec{
		Size:             "l",
		MaxWorkers:       int32Ptr(50),
		MinWorkers:       int32Ptr(5),
		Suspended:        true,
		AutoSuspendAfter: &duration,
		WakeMinWorkers:   int32Ptr(3),
		WakeTTL:          &duration,
		NodePool: &NodePoolSpec{
			MinNodes: int32Ptr(2),
			MaxNodes: int32Ptr(20),
			Zones:    []string{"zone-a", "zone-b"},
		},
		Routing: &RoutingSpec{
			// RoutingSpec is a simple struct
		},
		CatalogSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"team": "data-eng"},
		},
		ResourceGroupsProfile: "custom-profile",
		CustomConfigMaps:      []string{"config1", "config2", "config3"},
		Limits: &LimitsSpec{
			HardConcurrencyPerGroup: int32Ptr(10),
			MaxQueuedPerGroup:       int32Ptr(100),
		},
		ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
			"coordinator": map[string]interface{}{
				"resources": map[string]interface{}{
					"requests": map[string]string{"cpu": "2"},
				},
			},
		}),
		FaultTolerantExecution: &FaultTolerantExecutionSpec{
			RetryPolicy: "TASK",
			ExchangeManager: &ExchangeManagerSpec{
				Enabled:         boolPtr(true),
				Name:            "filesystem",
				BaseDirectories: []string{"s3://trino-exchange/runtime-a"},
				Properties: map[string]string{
					"exchange.s3.region": "us-east-1",
				},
			},
		},
		KEDA: &KEDASpec{
			Enabled:          boolPtr(true),
			ScalerType:       "prometheus",
			ScalingMetric:    "query",
			Threshold:        stringPtr("5"),
			PrometheusServer: stringPtr("http://prometheus:9090"),
		},
		TLS: &TLSSpec{
			ServerSecretClass:   "server-tls",
			InternalSecretClass: "internal-tls",
		},
		HelmChartConfig: &HelmChartConfigSpec{
			AccessControl: &AccessControlSpec{
				Rules: map[string]string{"admin": ".*"},
			},
		},
		OperatorNodePoolDefaults: &OperatorNodePoolDefaultsSpec{
			DefaultMinNodes: int32Ptr(1),
			DefaultMaxNodes: int32Ptr(10),
		},
		RolloutPolicy: &RolloutPolicySpec{
			RevisionHistoryLimit: int32Ptr(5),
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, spec.Size, copied.Size)
	assert.Equal(t, *spec.MaxWorkers, *copied.MaxWorkers)
	assert.Equal(t, *spec.MinWorkers, *copied.MinWorkers)
	assert.Equal(t, spec.Suspended, copied.Suspended)
	assert.Equal(t, spec.AutoSuspendAfter.Duration, copied.AutoSuspendAfter.Duration)
	assert.Equal(t, len(spec.NodePool.Zones), len(copied.NodePool.Zones))
	assert.Equal(t, len(spec.CustomConfigMaps), len(copied.CustomConfigMaps))
	assert.Equal(t, spec.ResourceGroupsProfile, copied.ResourceGroupsProfile)
	assert.Equal(t, spec.FaultTolerantExecution.RetryPolicy, copied.FaultTolerantExecution.RetryPolicy)
	assert.Equal(t, *spec.FaultTolerantExecution.ExchangeManager.Enabled, *copied.FaultTolerantExecution.ExchangeManager.Enabled)
	assert.Equal(t, spec.FaultTolerantExecution.ExchangeManager.BaseDirectories, copied.FaultTolerantExecution.ExchangeManager.BaseDirectories)
	assert.Equal(t, spec.FaultTolerantExecution.ExchangeManager.Properties, copied.FaultTolerantExecution.ExchangeManager.Properties)
	assert.Equal(t, *spec.KEDA.Enabled, *copied.KEDA.Enabled)
	assert.Equal(t, spec.KEDA.ScalerType, copied.KEDA.ScalerType)
	assert.Equal(t, spec.TLS.ServerSecretClass, copied.TLS.ServerSecretClass)
	specOverlay := spec.GetValuesOverlayMap()
	copyOverlay := copied.GetValuesOverlayMap()
	assert.Equal(t, len(specOverlay), len(copyOverlay))

	// Modify copy and verify original unchanged
	copied.Size = "xl"
	*copied.MaxWorkers = 100
	copied.NodePool.Zones = append(copied.NodePool.Zones, "zone-c")
	copied.CustomConfigMaps[0] = "modified"
	*copied.FaultTolerantExecution.ExchangeManager.Enabled = false
	copied.FaultTolerantExecution.ExchangeManager.BaseDirectories[0] = "s3://modified"
	copied.FaultTolerantExecution.ExchangeManager.Properties["exchange.s3.region"] = "eu-west-1"
	copyOverlay["new"] = "value"
	copyData, _ := json.Marshal(copyOverlay)
	copied.ValuesOverlay = &apiextensionsv1.JSON{Raw: copyData}
	assert.Equal(t, "l", spec.Size)
	assert.Equal(t, int32(50), *spec.MaxWorkers)
	assert.Equal(t, 2, len(spec.NodePool.Zones))
	assert.Equal(t, "config1", spec.CustomConfigMaps[0])
	assert.True(t, *spec.FaultTolerantExecution.ExchangeManager.Enabled)
	assert.Equal(t, "s3://trino-exchange/runtime-a", spec.FaultTolerantExecution.ExchangeManager.BaseDirectories[0])
	assert.Equal(t, "us-east-1", spec.FaultTolerantExecution.ExchangeManager.Properties["exchange.s3.region"])
	specOverlay = spec.GetValuesOverlayMap()
	_, exists := specOverlay["new"]
	assert.False(t, exists)
}

func TestNodePoolSpec_DeepCopy(t *testing.T) {
	duration := metav1.Duration{Duration: 10 * time.Minute}
	spec := &NodePoolSpec{
		MinNodes: int32Ptr(1),
		MaxNodes: int32Ptr(10),
		Zones:    []string{"zone-1", "zone-2", "zone-3"},
		OSDiskGB: int32Ptr(256),
		Spot: &SpotSpec{
			Enabled: true,
		},
		NodeLabels: map[string]string{
			"node-pool": "xtrinode-workers",
		},
		NodeTaints: []corev1.Taint{
			{
				Key:    "xtrinode",
				Value:  "true",
				Effect: corev1.TaintEffectNoSchedule,
			},
		},
		ResourceTags: map[string]string{
			"managed-by": "xtrinode-operator",
		},
		Prewarm: &PrewarmSpec{
			Nodes: int32Ptr(2),
			TTL:   &duration,
		},
		ProvisioningTimeout: &duration,
		Azure: &AzureNodePoolSpec{
			VMSize: "Standard_D8as_v5",
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, *spec.MinNodes, *copied.MinNodes)
	assert.Equal(t, len(spec.Zones), len(copied.Zones))
	assert.Equal(t, *spec.OSDiskGB, *copied.OSDiskGB)
	assert.Equal(t, spec.Spot.Enabled, copied.Spot.Enabled)
	assert.Equal(t, len(spec.NodeTaints), len(copied.NodeTaints))
	assert.Equal(t, len(spec.ResourceTags), len(copied.ResourceTags))
	assert.Equal(t, spec.Azure.VMSize, copied.Azure.VMSize)

	// Modify copy and verify original unchanged
	copied.Zones = append(copied.Zones, "zone-4")
	copied.NodeLabels["new"] = "label"
	copied.NodeTaints[0].Key = "modified"
	assert.Equal(t, 3, len(spec.Zones))
	_, exists := spec.NodeLabels["new"]
	assert.False(t, exists)
	assert.Equal(t, "xtrinode", spec.NodeTaints[0].Key)
}

func TestKEDASpec_DeepCopy(t *testing.T) {
	duration := metav1.Duration{Duration: 300 * time.Second}
	spec := &KEDASpec{
		Enabled:           boolPtr(true),
		ScalerType:        "http",
		ScalingMetric:     "memory",
		Threshold:         stringPtr("80"),
		PrometheusServer:  stringPtr("http://prometheus:9090"),
		PrometheusQuery:   stringPtr("sum(rate(trino_query_queued[1m]))"),
		HTTPEndpoint:      stringPtr("coordinator"),
		HTTPValueLocation: stringPtr("regex"),
		ScaleDownCooldown: &duration,
		ScaleUpCooldown:   &duration,
		JMXExporter: &JMXExporterSpec{
			Enabled: true,
			Port:    int32Ptr(5556),
			Image:   "jmx-exporter:latest",
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, *spec.Enabled, *copied.Enabled)
	assert.Equal(t, spec.ScalerType, copied.ScalerType)
	assert.Equal(t, *spec.Threshold, *copied.Threshold)
	assert.Equal(t, *spec.PrometheusServer, *copied.PrometheusServer)
	assert.Equal(t, spec.ScaleDownCooldown.Duration, copied.ScaleDownCooldown.Duration)
	assert.Equal(t, *spec.JMXExporter.Port, *copied.JMXExporter.Port)

	// Modify copy and verify original unchanged
	copied.ScalerType = "prometheus"
	*copied.Threshold = "100"
	copied.JMXExporter.Image = "modified"
	assert.Equal(t, "http", spec.ScalerType)
	assert.Equal(t, "80", *spec.Threshold)
	assert.Equal(t, "jmx-exporter:latest", spec.JMXExporter.Image)
}

func TestHelmChartConfigSpec_DeepCopy(t *testing.T) {
	spec := &HelmChartConfigSpec{
		AccessControl: &AccessControlSpec{
			Rules: map[string]string{
				"admin": ".*",
				"user":  "SELECT.*",
			},
		},
		SecretMounts: []SecretMountSpec{
			{Name: "secret1"},
			{Name: "secret2"},
		},
		Env: []corev1.EnvVar{
			{Name: "ENV_VAR", Value: "value"},
		},
		Coordinator: &CoordinatorHelmConfigSpec{
			SecretMounts: []SecretMountSpec{
				{Name: "coord-secret", SecretName: "coord-secret", Path: "/etc/coord"},
			},
		},
		Worker: &WorkerHelmConfigSpec{
			GracefulShutdown: &GracefulShutdownSpec{
				Enabled: true,
			},
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(spec.AccessControl.Rules), len(copied.AccessControl.Rules))
	assert.Equal(t, len(spec.SecretMounts), len(copied.SecretMounts))
	assert.Equal(t, len(spec.Env), len(copied.Env))
	assert.Equal(t, len(spec.Coordinator.SecretMounts), len(copied.Coordinator.SecretMounts))
	assert.Equal(t, spec.Worker.GracefulShutdown.Enabled, copied.Worker.GracefulShutdown.Enabled)

	// Modify copy and verify original unchanged
	copied.AccessControl.Rules["new"] = "rule"
	copied.SecretMounts[0].Name = "modified"
	copied.Env[0].Value = "modified"
	_, exists := spec.AccessControl.Rules["new"]
	assert.False(t, exists)
	assert.Equal(t, "secret1", spec.SecretMounts[0].Name)
	assert.Equal(t, "value", spec.Env[0].Value)
}

func TestLimitsSpec_DeepCopy(t *testing.T) {
	spec := &LimitsSpec{
		HardConcurrencyPerGroup: int32Ptr(20),
		MaxQueuedPerGroup:       int32Ptr(200),
		Session:                 &SessionLimits{
			// SessionLimits is a simple struct, just test it exists
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, *spec.HardConcurrencyPerGroup, *copied.HardConcurrencyPerGroup)
	assert.Equal(t, *spec.MaxQueuedPerGroup, *copied.MaxQueuedPerGroup)
	assert.NotNil(t, copied.Session)

	// Modify copy and verify original unchanged
	*copied.HardConcurrencyPerGroup = 50
	assert.Equal(t, int32(20), *spec.HardConcurrencyPerGroup)
}

func TestAllConnectorTypes_DeepCopy(t *testing.T) {
	// Test Hive connector
	hiveConnector := &XTrinodeCatalogConnector{
		Hive: &HiveCatalogSpec{
			MetastoreURI: "thrift://hive:9083",
			S3Endpoint:   "s3://bucket",
			Properties: map[string]string{
				"hive.metastore.uri": "thrift://hive:9083",
			},
		},
	}
	hiveCopy := hiveConnector.DeepCopy()
	assert.NotNil(t, hiveCopy)
	assert.Equal(t, hiveConnector.Hive.MetastoreURI, hiveCopy.Hive.MetastoreURI)
	assert.Equal(t, len(hiveConnector.Hive.Properties), len(hiveCopy.Hive.Properties))

	// Test Iceberg connector
	icebergConnector := &XTrinodeCatalogConnector{
		Iceberg: &IcebergCatalogSpec{
			CatalogType:  "hive",
			WarehouseURI: "s3://warehouse",
			Properties: map[string]string{
				"warehouse": "s3://warehouse",
			},
		},
	}
	icebergCopy := icebergConnector.DeepCopy()
	assert.NotNil(t, icebergCopy)
	assert.Equal(t, icebergConnector.Iceberg.CatalogType, icebergCopy.Iceberg.CatalogType)
	assert.Equal(t, icebergConnector.Iceberg.WarehouseURI, icebergCopy.Iceberg.WarehouseURI)

	// Test DeltaLake connector
	deltaConnector := &XTrinodeCatalogConnector{
		DeltaLake: &DeltaLakeCatalogSpec{
			CatalogType:  "hive",
			WarehouseURI: "s3://delta",
			Properties: map[string]string{
				"delta.warehouse": "s3://delta",
			},
		},
	}
	deltaCopy := deltaConnector.DeepCopy()
	assert.NotNil(t, deltaCopy)
	assert.Equal(t, deltaConnector.DeltaLake.WarehouseURI, deltaCopy.DeltaLake.WarehouseURI)

	// Test MySQL connector
	mysqlConnector := &XTrinodeCatalogConnector{
		MySQL: &MySQLCatalogSpec{
			ConnectionURL:  "jdbc:mysql://mysql:3306/db",
			ConnectionUser: "user",
			ConnectionPasswordSecret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "mysql-secret",
				},
				Key: "password",
			},
			Properties: map[string]string{
				"connection-url": "jdbc:mysql://mysql:3306/db",
			},
		},
	}
	mysqlCopy := mysqlConnector.DeepCopy()
	assert.NotNil(t, mysqlCopy)
	assert.Equal(t, mysqlConnector.MySQL.ConnectionURL, mysqlCopy.MySQL.ConnectionURL)
	assert.Equal(t, mysqlConnector.MySQL.ConnectionUser, mysqlCopy.MySQL.ConnectionUser)
	assert.Equal(t, mysqlConnector.MySQL.ConnectionPasswordSecret.Name, mysqlCopy.MySQL.ConnectionPasswordSecret.Name)

	// Test MongoDB connector
	mongoConnector := &XTrinodeCatalogConnector{
		MongoDB: &MongoDBCatalogSpec{
			ConnectionURI: "mongodb://mongo:27017",
			Properties: map[string]string{
				"mongodb.uri": "mongodb://mongo:27017",
			},
		},
	}
	mongoCopy := mongoConnector.DeepCopy()
	assert.NotNil(t, mongoCopy)
	assert.Equal(t, mongoConnector.MongoDB.ConnectionURI, mongoCopy.MongoDB.ConnectionURI)
}

func TestXTrinodeCatalogSpec_DeepCopy_WithLabels(t *testing.T) {
	spec := &XTrinodeCatalogSpec{
		Connector: XTrinodeCatalogConnector{
			Postgres: &PostgresCatalogSpec{
				ConnectionURL: "jdbc:postgresql://localhost:5432/test",
				Properties: map[string]string{
					"connection-url": "jdbc:postgresql://localhost:5432/test",
				},
			},
		},
		Labels: map[string]string{
			"team":        "data-eng",
			"environment": "prod",
			"region":      "us-east-1",
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(spec.Labels), len(copied.Labels))
	assert.Equal(t, spec.Labels["team"], copied.Labels["team"])
	assert.Equal(t, spec.Connector.Postgres.ConnectionURL, copied.Connector.Postgres.ConnectionURL)

	// Modify copy and verify original unchanged
	copied.Labels["new"] = "label"
	copied.Connector.Postgres.ConnectionURL = "modified"
	_, exists := spec.Labels["new"]
	assert.False(t, exists)
	assert.Equal(t, "jdbc:postgresql://localhost:5432/test", spec.Connector.Postgres.ConnectionURL)
}

func TestXTrinodeCatalogStatus_DeepCopy_WithLastUpdated(t *testing.T) {
	now := metav1.Now()
	status := &XTrinodeCatalogStatus{
		Phase:         "Ready",
		Message:       "Catalog configured successfully",
		ConfigMapName: "trino-catalog-test",
		LastUpdated:   &now,
	}

	copied := status.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, status.Phase, copied.Phase)
	assert.Equal(t, status.Message, copied.Message)
	assert.Equal(t, status.ConfigMapName, copied.ConfigMapName)
	assert.NotNil(t, copied.LastUpdated)
	assert.Equal(t, status.LastUpdated.Time, copied.LastUpdated.Time)

	// Modify copy and verify original unchanged
	copied.Phase = "Error"
	copied.Message = "Modified"
	assert.Equal(t, "Ready", status.Phase)
	assert.Equal(t, "Catalog configured successfully", status.Message)
}

func TestRolloutPolicySpec_DeepCopy(t *testing.T) {
	spec := &RolloutPolicySpec{
		RevisionHistoryLimit: int32Ptr(10),
		RollingUpdateStrategy: &RollingUpdateStrategySpec{
			MaxSurge:       intstrPtr(intstr.FromInt(1)),
			MaxUnavailable: intstrPtr(intstr.FromInt(0)),
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, *spec.RevisionHistoryLimit, *copied.RevisionHistoryLimit)
	assert.NotNil(t, copied.RollingUpdateStrategy)
	assert.Equal(t, spec.RollingUpdateStrategy.MaxSurge.IntVal, copied.RollingUpdateStrategy.MaxSurge.IntVal)

	// Modify copy and verify original unchanged
	*copied.RevisionHistoryLimit = 20
	copied.RollingUpdateStrategy.MaxSurge = intstrPtr(intstr.FromInt(2))
	assert.Equal(t, int32(10), *spec.RevisionHistoryLimit)
	assert.Equal(t, int32(1), spec.RollingUpdateStrategy.MaxSurge.IntVal)
}

// Helper functions
func int32Ptr(i int32) *int32 {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func stringPtr(s string) *string {
	return &s
}

func intstrPtr(i intstr.IntOrString) *intstr.IntOrString {
	return &i
}

func TestCoordinatorHelmConfigSpec_DeepCopy(t *testing.T) {
	spec := &CoordinatorHelmConfigSpec{
		SecretMounts: []SecretMountSpec{
			{Name: "secret1", SecretName: "secret1", Path: "/etc/secret1"},
			{Name: "secret2", SecretName: "secret2", Path: "/etc/secret2"},
		},
		AdditionalConfigFiles: map[string]string{
			"config1.properties": "content1",
			"config2.properties": "content2",
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(spec.SecretMounts), len(copied.SecretMounts))
	assert.Equal(t, len(spec.AdditionalConfigFiles), len(copied.AdditionalConfigFiles))
	assert.Equal(t, spec.AdditionalConfigFiles["config1.properties"], copied.AdditionalConfigFiles["config1.properties"])

	// Modify copy and verify original unchanged
	copied.SecretMounts[0].Name = "modified"
	copied.AdditionalConfigFiles["new"] = "new-content"
	assert.Equal(t, "secret1", spec.SecretMounts[0].Name)
	_, exists := spec.AdditionalConfigFiles["new"]
	assert.False(t, exists)
}

func TestWorkerHelmConfigSpec_DeepCopy(t *testing.T) {
	spec := &WorkerHelmConfigSpec{
		GracefulShutdown: &GracefulShutdownSpec{
			Enabled: true,
		},
		SecretMounts: []SecretMountSpec{
			{Name: "worker-secret", SecretName: "worker-secret", Path: "/etc/worker"},
		},
		AdditionalConfigFiles: map[string]string{
			"worker.properties": "worker-content",
		},
		TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
			{
				MaxSkew:           1,
				TopologyKey:       "zone",
				WhenUnsatisfiable: corev1.DoNotSchedule,
			},
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, spec.GracefulShutdown.Enabled, copied.GracefulShutdown.Enabled)
	assert.Equal(t, len(spec.SecretMounts), len(copied.SecretMounts))
	assert.Equal(t, len(spec.TopologySpreadConstraints), len(copied.TopologySpreadConstraints))

	// Modify copy and verify original unchanged
	copied.GracefulShutdown.Enabled = false
	copied.AdditionalConfigFiles["new"] = "new"
	assert.Equal(t, true, spec.GracefulShutdown.Enabled)
	_, exists := spec.AdditionalConfigFiles["new"]
	assert.False(t, exists)
}

func TestIngressSpec_DeepCopy(t *testing.T) {
	spec := &IngressSpec{
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/ssl-redirect": "true",
		},
		Hosts: []IngressHostSpec{
			{
				Host: "trino.example.com",
				Paths: []IngressPathSpec{
					{Path: "/", PathType: "Prefix"},
				},
			},
		},
		TLS: []IngressTLSSpec{
			{
				SecretName: "tls-secret",
				Hosts:      []string{"trino.example.com"},
			},
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(spec.Annotations), len(copied.Annotations))
	assert.Equal(t, len(spec.Hosts), len(copied.Hosts))
	assert.Equal(t, len(spec.TLS), len(copied.TLS))
	assert.Equal(t, spec.Hosts[0].Host, copied.Hosts[0].Host)
	assert.Equal(t, len(spec.Hosts[0].Paths), len(copied.Hosts[0].Paths))

	// Modify copy and verify original unchanged
	copied.Annotations["new"] = "annotation"
	copied.Hosts[0].Host = "modified.example.com"
	_, exists := spec.Annotations["new"]
	assert.False(t, exists)
	assert.Equal(t, "trino.example.com", spec.Hosts[0].Host)
}

func TestNetworkPolicySpec_DeepCopy(t *testing.T) {
	spec := &NetworkPolicySpec{
		Ingress: []NetworkPolicyIngressSpec{
			{
				From: []NetworkPolicyPeerSpec{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "trino"},
						},
					},
				},
				Ports: []NetworkPolicyPortSpec{
					{
						Protocol: func() *corev1.Protocol { p := corev1.ProtocolTCP; return &p }(),
						Port:     intstrPtr(intstr.FromInt(8080)),
					},
				},
			},
		},
		Egress: []NetworkPolicyEgressSpec{
			{
				To: []NetworkPolicyPeerSpec{
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"name": "default"},
						},
					},
				},
			},
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(spec.Ingress), len(copied.Ingress))
	assert.Equal(t, len(spec.Egress), len(copied.Egress))
	assert.Equal(t, len(spec.Ingress[0].From), len(copied.Ingress[0].From))
	assert.Equal(t, len(spec.Ingress[0].Ports), len(copied.Ingress[0].Ports))

	// Modify copy and verify original unchanged
	copied.Ingress[0].From[0].PodSelector.MatchLabels["new"] = "label"
	_, exists := spec.Ingress[0].From[0].PodSelector.MatchLabels["new"]
	assert.False(t, exists)
}

func TestServiceMonitorSpec_DeepCopy(t *testing.T) {
	spec := &ServiceMonitorSpec{
		Labels: map[string]string{
			"monitor": "trino",
		},
		Coordinator: &ServiceMonitorRoleSpec{
			Labels: map[string]string{"coord": "true"},
		},
		Worker: &ServiceMonitorRoleSpec{
			Labels: map[string]string{"worker": "true"},
		},
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, len(spec.Labels), len(copied.Labels))
	assert.NotNil(t, copied.Coordinator)
	assert.NotNil(t, copied.Worker)
	assert.Equal(t, len(spec.Coordinator.Labels), len(copied.Coordinator.Labels))
	assert.Equal(t, len(spec.Worker.Labels), len(copied.Worker.Labels))

	// Modify copy and verify original unchanged
	copied.Labels["new"] = "label"
	copied.Coordinator.Labels["new"] = "coord-label"
	_, exists := spec.Labels["new"]
	assert.False(t, exists)
	_, exists = spec.Coordinator.Labels["new"]
	assert.False(t, exists)
}

func TestPrewarmSpec_DeepCopy(t *testing.T) {
	duration := metav1.Duration{Duration: 30 * time.Minute}
	spec := &PrewarmSpec{
		Nodes: int32Ptr(5),
		TTL:   &duration,
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, *spec.Nodes, *copied.Nodes)
	assert.Equal(t, spec.TTL.Duration, copied.TTL.Duration)

	// Modify copy and verify original unchanged
	*copied.Nodes = 10
	assert.Equal(t, int32(5), *spec.Nodes)
}

func TestXTrinodeSpec_DeepCopy_EmptyFields(t *testing.T) {
	// Test with minimal fields to cover empty/nil cases
	spec := &XTrinodeSpec{
		Size: "s",
		// All other fields nil/empty
	}

	copied := spec.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, spec.Size, copied.Size)
	assert.Nil(t, copied.MaxWorkers)
	assert.Nil(t, copied.MinWorkers)
	assert.Nil(t, copied.NodePool)
	assert.Nil(t, copied.KEDA)
	assert.Nil(t, copied.GetValuesOverlayMap())
}

func TestXTrinodeStatus_DeepCopy_EmptyFields(t *testing.T) {
	// Test with minimal fields
	status := &XTrinodeStatus{
		Phase: "Pending",
		// All other fields nil/empty
	}

	copied := status.DeepCopy()
	assert.NotNil(t, copied)
	assert.Equal(t, status.Phase, copied.Phase)
	assert.Equal(t, int32(0), copied.Workers)
	assert.Nil(t, copied.LastActivity)
	assert.Nil(t, copied.Conditions)
	assert.Equal(t, "", copied.CurrentRevision)
}
