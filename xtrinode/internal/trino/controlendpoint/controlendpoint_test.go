package controlendpoint

import (
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCoordinatorURLUsesDefaultHTTPPort(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
	}

	require.Equal(t, "http://trino-runtime.team-a.svc.cluster.local:8080", CoordinatorURL(xtrinode))
}

func TestCoordinatorURLUsesValuesOverlayServicePort(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: &apiextensionsv1.JSON{
				Raw: []byte(`{"service":{"port":8181}}`),
			},
		},
	}

	require.Equal(t, 8181, HTTPPort(xtrinode))
	require.Equal(t, "http://trino-runtime.team-a.svc.cluster.local:8181", CoordinatorURL(xtrinode))
}
