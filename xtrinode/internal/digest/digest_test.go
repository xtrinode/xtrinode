package digest

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDigest_Deterministic(t *testing.T) {
	// Same inputs should produce same hash
	d1 := New()
	d1.AddString("key1", "value1")
	d1.AddString("key2", "value2")
	hash1 := d1.Sum12()

	d2 := New()
	d2.AddString("key1", "value1")
	d2.AddString("key2", "value2")
	hash2 := d2.Sum12()

	if hash1 != hash2 {
		t.Errorf("Expected deterministic hash, got %s != %s", hash1, hash2)
	}

	if len(hash1) != 12 {
		t.Errorf("Expected 12 character hash, got %d", len(hash1))
	}
}

func TestDigest_OrderMatters(t *testing.T) {
	// Different order should produce different hash
	d1 := New()
	d1.AddString("key1", "value1")
	d1.AddString("key2", "value2")
	hash1 := d1.Sum12()

	d2 := New()
	d2.AddString("key2", "value2")
	d2.AddString("key1", "value1")
	hash2 := d2.Sum12()

	if hash1 == hash2 {
		t.Errorf("Expected different hashes for different order, got %s == %s", hash1, hash2)
	}
}

func TestConfigMapDataDigest_Empty(t *testing.T) {
	// Nil ConfigMap
	hash := ConfigMapDataDigest(nil)
	if hash != "" {
		t.Errorf("Expected empty hash for nil ConfigMap, got %s", hash)
	}

	// Empty data
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Data:       map[string]string{},
	}
	hash = ConfigMapDataDigest(cm)
	if hash != "" {
		t.Errorf("Expected empty hash for empty data, got %s", hash)
	}
}

func TestConfigMapDataDigest_Content(t *testing.T) {
	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test1"},
		Data: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test2"}, // Different name
		Data: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	hash1 := ConfigMapDataDigest(cm1)
	hash2 := ConfigMapDataDigest(cm2)

	// Same data should produce same hash regardless of name
	if hash1 != hash2 {
		t.Errorf("Expected same hash for same data, got %s != %s", hash1, hash2)
	}

	// Different data should produce different hash
	cm3 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test3"},
		Data: map[string]string{
			"key1": "value1",
			"key2": "different",
		},
	}
	hash3 := ConfigMapDataDigest(cm3)
	if hash1 == hash3 {
		t.Errorf("Expected different hash for different data, got %s == %s", hash1, hash3)
	}
}

func TestConfigMapDataDigest_KeyOrder(t *testing.T) {
	// Keys are sorted, so order in map doesn't matter
	cm1 := &corev1.ConfigMap{
		Data: map[string]string{
			"a": "1",
			"b": "2",
			"c": "3",
		},
	}

	cm2 := &corev1.ConfigMap{
		Data: map[string]string{
			"c": "3",
			"a": "1",
			"b": "2",
		},
	}

	hash1 := ConfigMapDataDigest(cm1)
	hash2 := ConfigMapDataDigest(cm2)

	if hash1 != hash2 {
		t.Errorf("Expected same hash regardless of key order, got %s != %s", hash1, hash2)
	}
}

func TestConfigMapListDigest(t *testing.T) {
	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm1"},
		Data:       map[string]string{"key": "value1"},
	}
	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm2"},
		Data:       map[string]string{"key": "value2"},
	}

	// Empty list
	hash := ConfigMapListDigest([]*corev1.ConfigMap{})
	if hash != "" {
		t.Errorf("Expected empty hash for empty list, got %s", hash)
	}

	// Single ConfigMap
	hash1 := ConfigMapListDigest([]*corev1.ConfigMap{cm1})
	if hash1 == "" {
		t.Error("Expected non-empty hash for single ConfigMap")
	}

	// Multiple ConfigMaps
	hash2 := ConfigMapListDigest([]*corev1.ConfigMap{cm1, cm2})
	if hash2 == "" {
		t.Error("Expected non-empty hash for multiple ConfigMaps")
	}

	// Different order should produce different hash (list order matters)
	hash3 := ConfigMapListDigest([]*corev1.ConfigMap{cm2, cm1})
	if hash2 == hash3 {
		t.Errorf("Expected different hash for different order, got %s == %s", hash2, hash3)
	}
}

func TestSecretDataDigest(t *testing.T) {
	secret1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secret1"},
		Data: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	secret2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secret2"},
		Data: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	hash1 := SecretDataDigest(secret1)
	hash2 := SecretDataDigest(secret2)

	// Same data should produce same hash
	if hash1 != hash2 {
		t.Errorf("Expected same hash for same data, got %s != %s", hash1, hash2)
	}

	// Empty secret
	emptySecret := &corev1.Secret{Data: map[string][]byte{}}
	hash := SecretDataDigest(emptySecret)
	if hash != "" {
		t.Errorf("Expected empty hash for empty secret, got %s", hash)
	}
}

func TestResourceVersionDigest(t *testing.T) {
	// Empty list
	hash := ResourceVersionDigest()
	if hash != "" {
		t.Errorf("Expected empty hash for no versions, got %s", hash)
	}

	// Single version
	hash1 := ResourceVersionDigest("12345")
	if hash1 == "" {
		t.Error("Expected non-empty hash for single version")
	}

	// Multiple versions
	hash2 := ResourceVersionDigest("12345", "67890")
	if hash2 == "" {
		t.Error("Expected non-empty hash for multiple versions")
	}

	// Same versions should produce same hash
	hash3 := ResourceVersionDigest("12345", "67890")
	if hash2 != hash3 {
		t.Errorf("Expected same hash for same versions, got %s != %s", hash2, hash3)
	}

	// Different order should produce different hash
	hash4 := ResourceVersionDigest("67890", "12345")
	if hash2 == hash4 {
		t.Errorf("Expected different hash for different order, got %s == %s", hash2, hash4)
	}
}

func TestDigest_AddJSON(t *testing.T) {
	type testStruct struct {
		Field1 string
		Field2 int
	}

	d1 := New()
	d1.AddJSON("data", testStruct{Field1: "value", Field2: 42})
	hash1 := d1.Sum12()

	d2 := New()
	d2.AddJSON("data", testStruct{Field1: "value", Field2: 42})
	hash2 := d2.Sum12()

	if hash1 != hash2 {
		t.Errorf("Expected same hash for same JSON data, got %s != %s", hash1, hash2)
	}

	// Different data
	d3 := New()
	d3.AddJSON("data", testStruct{Field1: "different", Field2: 42})
	hash3 := d3.Sum12()

	if hash1 == hash3 {
		t.Errorf("Expected different hash for different JSON data, got %s == %s", hash1, hash3)
	}
}
