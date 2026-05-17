package resources

import (
	"encoding/json"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// mustValuesOverlay converts a map[string]interface{} to *apiextensionsv1.JSON for testing
// Panics on error since this is only used in test setup
func mustValuesOverlay(m map[string]interface{}) *apiextensionsv1.JSON {
	if m == nil {
		return nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return &apiextensionsv1.JSON{Raw: data}
}
