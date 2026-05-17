package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	corev1 "k8s.io/api/core/v1"
)

// Digest provides deterministic hashing for rollout detection
type Digest struct {
	h []byte
}

// New creates a new Digest
func New() *Digest {
	return &Digest{h: []byte{}}
}

// AddString adds a key-value string pair to the digest
func (d *Digest) AddString(k, v string) {
	d.h = append(d.h, []byte(k)...)
	d.h = append(d.h, 0)
	d.h = append(d.h, []byte(v)...)
	d.h = append(d.h, 0)
}

// AddJSON adds a key and JSON-marshaled value to the digest
// Uses deterministic JSON encoding (map keys are sorted)
func (d *Digest) AddJSON(k string, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("null")
	}
	d.AddBytes(k, b)
}

// AddBytes adds a key and byte slice to the digest
func (d *Digest) AddBytes(k string, b []byte) {
	d.h = append(d.h, []byte(k)...)
	d.h = append(d.h, 0)
	d.h = append(d.h, b...)
	d.h = append(d.h, 0)
}

// Sum12 returns the first 12 hex characters of the SHA256 hash
func (d *Digest) Sum12() string {
	hash := sha256.Sum256(d.h)
	return hex.EncodeToString(hash[:])[:12]
}

// ConfigMapDataDigest computes a deterministic digest from ConfigMap data
// Returns empty string if ConfigMap is nil or has no data
func ConfigMapDataDigest(cm *corev1.ConfigMap) string {
	if cm == nil || len(cm.Data) == 0 {
		return ""
	}

	d := New()
	keys := make([]string, 0, len(cm.Data))
	for k := range cm.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d.AddString(k, cm.Data[k])
	}
	return d.Sum12()
}

// ConfigMapListDigest computes a digest from multiple ConfigMaps
// Returns empty string if list is empty or all ConfigMaps are nil
func ConfigMapListDigest(cms []*corev1.ConfigMap) string {
	if len(cms) == 0 {
		return ""
	}

	d := New()
	for _, cm := range cms {
		if cm != nil {
			d.AddString("name", cm.Name)
			d.AddString("digest", ConfigMapDataDigest(cm))
		}
	}
	return d.Sum12()
}

// SecretDataDigest computes a deterministic digest from Secret data
// Returns empty string if Secret is nil or has no data
func SecretDataDigest(secret *corev1.Secret) string {
	if secret == nil || len(secret.Data) == 0 {
		return ""
	}

	d := New()
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d.AddBytes(k, secret.Data[k])
	}
	return d.Sum12()
}

// ResourceVersionDigest computes a digest from resource versions (cheaper alternative)
// Useful when you don't want to read full ConfigMap/Secret data
func ResourceVersionDigest(resourceVersions ...string) string {
	if len(resourceVersions) == 0 {
		return ""
	}

	d := New()
	for i, rv := range resourceVersions {
		if rv != "" {
			d.AddString(string(rune(i)), rv)
		}
	}
	return d.Sum12()
}
