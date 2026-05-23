package rollout

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/catalog"
	"github.com/xtrinode/xtrinode/internal/digest"
)

// RolloutInputs contains all inputs for computing rollout hashes
type RolloutInputs struct {
	RenderRevision string

	// Content digests from external dependencies
	CatalogDigest       string
	AccessControlDigest string
	SessionPropsDigest  string
	SecretDigest        string

	// Effective configs (after defaults + overlay applied)
	CoordConfig  interface{}
	WorkerConfig interface{}
}

// CoordinatorRolloutHash computes the rollout hash for coordinator deployment
// Includes catalog digest since coordinator serves catalog metadata
func CoordinatorRolloutHash(in RolloutInputs) string { //nolint:gocritic // hugeParam: passing by pointer would break callers that construct RolloutInputs inline
	d := digest.New()
	d.AddString("render", in.RenderRevision)
	d.AddJSON("coordCfg", in.CoordConfig)
	d.AddString("access", in.AccessControlDigest)
	d.AddString("session", in.SessionPropsDigest)
	d.AddString("secret", in.SecretDigest)
	d.AddString("catalog", in.CatalogDigest) // Coordinator always includes catalogs
	return d.Sum12()
}

// WorkerRolloutHash computes the rollout hash for worker deployment
// By default, excludes catalog digest (workers don't need catalog metadata changes)
// Set rollWorkersOnCatalogChange=true to include catalog digest
func WorkerRolloutHash(in RolloutInputs, rollWorkersOnCatalogChange bool) string { //nolint:gocritic // hugeParam: passing by pointer would break callers that construct RolloutInputs inline
	d := digest.New()
	d.AddString("render", in.RenderRevision)
	d.AddJSON("workerCfg", in.WorkerConfig)
	d.AddString("access", in.AccessControlDigest)
	d.AddString("session", in.SessionPropsDigest)
	d.AddString("secret", in.SecretDigest)
	if rollWorkersOnCatalogChange {
		d.AddString("catalog", in.CatalogDigest)
	}
	return d.Sum12()
}

// ComputeCatalogDigest fetches catalog ConfigMaps and computes their content digest
func ComputeCatalogDigest(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, catalogs []string) (string, error) {
	// Fetch each catalog ConfigMap and compute digest
	configMaps := make([]*corev1.ConfigMap, 0, len(catalogs))
	for _, catalogName := range catalogs {
		cm := &corev1.ConfigMap{}
		key := client.ObjectKey{
			Namespace: xtrinode.Namespace,
			Name:      fmt.Sprintf("trino-catalog-%s", catalogName),
		}
		if err := cli.Get(ctx, key, cm); err != nil {
			if apierrors.IsNotFound(err) {
				// ConfigMap doesn't exist yet - skip it (teams may create later)
				// Don't fail - just compute digest from what exists
				continue
			}
			// Return error for other failures (RBAC, timeout, API server issues)
			// This prevents silently producing a "stable" digest when we're failing to read catalogs
			return "", fmt.Errorf("failed to fetch catalog ConfigMap %s: %w", catalogName, err)
		}
		configMaps = append(configMaps, cm)
	}

	return digest.ConfigMapListDigest(configMaps), nil
}

// ComputeAccessControlDigest computes digest from access control configuration
// Access control can be inline (in spec) or reference external ConfigMaps
func ComputeAccessControlDigest(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) (string, error) {
	// Check if access control is configured in HelmChartConfig
	if xtrinode.Spec.HelmChartConfig == nil || xtrinode.Spec.HelmChartConfig.AccessControl == nil {
		return "", nil
	}

	ac := xtrinode.Spec.HelmChartConfig.AccessControl
	d := digest.New()

	// Hash the access control type and configuration
	d.AddString("type", ac.Type)
	d.AddString("refreshPeriod", ac.RefreshPeriod)
	d.AddString("configFile", ac.ConfigFile)

	// Hash inline rules if present
	if len(ac.Rules) > 0 {
		d.AddJSON("rules", ac.Rules)
	}

	// Hash properties if present
	if ac.Properties != "" {
		d.AddString("properties", ac.Properties)
	}

	// If type is configmap and rules are provided inline, the operator creates the ConfigMap
	// So we hash the inline content, not fetch external ConfigMaps
	// External ConfigMaps would be referenced via catalog system

	return d.Sum12(), nil
}

// ComputeSessionPropsDigest computes digest from session properties configuration
// Session properties are in valuesOverlay["sessionProperties"] (root level)
func ComputeSessionPropsDigest(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) (string, error) {
	// Check if session properties are configured in valuesOverlay (root level)
	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap == nil {
		return "", nil
	}

	sessionProps, ok := valuesMap["sessionProperties"].(map[string]interface{})
	if !ok {
		return "", nil
	}

	d := digest.New()

	// Hash the session properties type and configuration
	if sessionType, ok := sessionProps["type"].(string); ok {
		d.AddString("type", sessionType)

		switch sessionType {
		case "configmap":
			// Hash inline session properties config
			if sessionPropertiesConfig, ok := sessionProps["sessionPropertiesConfig"].(string); ok {
				d.AddString("sessionPropertiesConfig", sessionPropertiesConfig)
			}
		case "properties":
			// Hash properties string
			if properties, ok := sessionProps["properties"].(string); ok {
				d.AddString("properties", properties)
			}
		}
	}

	return d.Sum12(), nil
}

// ComputeSecretDigest computes digest from external data sources that should trigger rollouts
// Includes: TLS secrets, ImagePullSecrets, SecretMounts (global/coordinator/worker),
// mounted ConfigMaps from runtime customization, auth secrets (passwordAuth, groupsAuth),
// and catalog secret references.
// Exported for use in builder
func ComputeSecretDigest(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) (string, error) {
	d := digest.New()

	hasExternalData, err := addConfiguredSecretDigests(ctx, cli, xtrinode, d)
	if err != nil {
		return "", err
	}

	mountedResourcesFound, err := addMountedResourceDigests(ctx, cli, xtrinode, d)
	if err != nil {
		return "", err
	}
	hasExternalData = hasExternalData || mountedResourcesFound

	catalogSecretsFound, err := addCatalogSecretDigests(ctx, cli, xtrinode, d)
	if err != nil {
		return "", err
	}
	hasExternalData = hasExternalData || catalogSecretsFound

	if !hasExternalData {
		return "", nil
	}

	return d.Sum12(), nil
}

func addConfiguredSecretDigests(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, d *digest.Digest) (bool, error) {
	hasExternalData, err := addTLSSecretDigests(ctx, cli, xtrinode.Namespace, xtrinode.Spec.TLS, d)
	if err != nil {
		return false, err
	}

	hasExternalData = addImagePullSecretRefs(xtrinode.Spec.HelmChartConfig, d) || hasExternalData

	secretMountsFound, err := addHelmSecretMountDigests(ctx, cli, xtrinode.Namespace, xtrinode.Spec.HelmChartConfig, d)
	if err != nil {
		return false, err
	}
	hasExternalData = secretMountsFound || hasExternalData

	authSecretsFound, err := addAuthSecretDigests(ctx, cli, xtrinode.Namespace, xtrinode.Spec.GetValuesOverlayMap(), d)
	if err != nil {
		return false, err
	}
	hasExternalData = authSecretsFound || hasExternalData

	controlAuthFound, err := addTrinoControlAuthSecretDigest(ctx, cli, xtrinode, d)
	if err != nil {
		return false, err
	}
	hasExternalData = controlAuthFound || hasExternalData

	envSecretFound, err := addHelmEnvSecretDigests(ctx, cli, xtrinode.Namespace, xtrinode.Spec.HelmChartConfig, d)
	if err != nil {
		return false, err
	}
	return envSecretFound || hasExternalData, nil
}

func addTLSSecretDigests(ctx context.Context, cli client.Client, namespace string, tls *analyticsv1.TLSSpec, d *digest.Digest) (bool, error) {
	if tls == nil {
		return false, nil
	}

	secretRefs := []struct {
		digestKey string
		name      string
		prefix    string
	}{
		{digestKey: "tls-server-secret-name", name: tls.ServerSecretClass, prefix: "tls-server-secret"},
		{digestKey: "tls-internal-secret-name", name: tls.InternalSecretClass, prefix: "tls-internal-secret"},
	}

	found := false
	for _, ref := range secretRefs {
		if ref.name == "" {
			continue
		}
		d.AddString(ref.digestKey, ref.name)
		if err := hashSecretData(ctx, cli, namespace, ref.name, ref.prefix, d); err != nil {
			return false, err
		}
		found = true
	}
	return found, nil
}

func addImagePullSecretRefs(cfg *analyticsv1.HelmChartConfigSpec, d *digest.Digest) bool {
	if cfg == nil || len(cfg.ImagePullSecrets) == 0 {
		return false
	}
	for _, ips := range cfg.ImagePullSecrets {
		d.AddString("imagePullSecret", ips.Name)
	}
	return true
}

func addHelmSecretMountDigests(ctx context.Context, cli client.Client, namespace string, cfg *analyticsv1.HelmChartConfigSpec, d *digest.Digest) (bool, error) {
	if cfg == nil {
		return false, nil
	}

	mountGroups := []struct {
		prefix string
		mounts []analyticsv1.SecretMountSpec
	}{
		{prefix: "global-", mounts: cfg.SecretMounts},
	}
	if cfg.Coordinator != nil {
		mountGroups = append(mountGroups, struct {
			prefix string
			mounts []analyticsv1.SecretMountSpec
		}{prefix: "coord-", mounts: cfg.Coordinator.SecretMounts})
	}
	if cfg.Worker != nil {
		mountGroups = append(mountGroups, struct {
			prefix string
			mounts []analyticsv1.SecretMountSpec
		}{prefix: "worker-", mounts: cfg.Worker.SecretMounts})
	}

	found := false
	for _, group := range mountGroups {
		for _, secretMount := range group.mounts {
			if err := hashSecretData(ctx, cli, namespace, secretMount.SecretName, group.prefix+secretMount.Name, d); err != nil {
				return false, err
			}
			found = true
		}
	}
	return found, nil
}

func addAuthSecretDigests(ctx context.Context, cli client.Client, namespace string, valuesMap map[string]interface{}, d *digest.Digest) (bool, error) {
	if valuesMap == nil {
		return false, nil
	}

	auth, ok := valuesMap["auth"].(map[string]interface{})
	if !ok {
		return false, nil
	}

	found := false
	for _, ref := range []struct {
		key       string
		digestKey string
	}{
		{key: "passwordAuthSecret", digestKey: "auth-password-secret"},
		{key: "groupsAuthSecret", digestKey: "auth-groups-secret"},
	} {
		if secretName, ok := auth[ref.key].(string); ok && secretName != "" {
			d.AddString(ref.digestKey, secretName)
			if err := hashSecretData(ctx, cli, namespace, secretName, ref.digestKey+"-data", d); err != nil {
				return false, err
			}
			found = true
		}
	}
	return found, nil
}

func addTrinoControlAuthSecretDigest(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, d *digest.Digest) (bool, error) {
	if xtrinode.Spec.TrinoControlAuth == nil || xtrinode.Spec.TrinoControlAuth.PasswordSecret == nil {
		return false, nil
	}
	if err := hashSecretKeyData(ctx, cli, xtrinode.Namespace, "trino-control-auth", xtrinode.Spec.TrinoControlAuth.PasswordSecret, d); err != nil {
		return false, err
	}
	return true, nil
}

func addHelmEnvSecretDigests(ctx context.Context, cli client.Client, namespace string, cfg *analyticsv1.HelmChartConfigSpec, d *digest.Digest) (bool, error) {
	if cfg == nil {
		return false, nil
	}
	found := false
	for _, envVar := range cfg.Env {
		if envVar.ValueFrom == nil || envVar.ValueFrom.SecretKeyRef == nil {
			continue
		}
		if envVar.ValueFrom.SecretKeyRef.Name == "" {
			continue
		}
		if err := hashSecretKeyData(ctx, cli, namespace, "helm-env-"+envVar.Name, envVar.ValueFrom.SecretKeyRef, d); err != nil {
			return false, err
		}
		found = true
	}
	return found, nil
}

func addMountedResourceDigests(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, d *digest.Digest) (bool, error) {
	found := false
	for _, configMapName := range sortedUniqueStrings(externalConfigMapReferences(xtrinode)) {
		d.AddString("mounted-configmap-name", configMapName)
		if err := hashConfigMapData(ctx, cli, xtrinode.Namespace, configMapName, "mounted-configmap-"+configMapName, d); err != nil {
			return false, err
		}
		found = true
	}
	for _, secretName := range sortedUniqueStrings(externalSecretReferences(xtrinode)) {
		d.AddString("mounted-secret-name", secretName)
		if err := hashSecretData(ctx, cli, xtrinode.Namespace, secretName, "mounted-secret-"+secretName, d); err != nil {
			return false, err
		}
		found = true
	}
	return found, nil
}

func externalConfigMapReferences(xtrinode *analyticsv1.XTrinode) []string {
	refs := make([]string, 0)
	if xtrinode.Spec.ResourceGroupsProfile != "" {
		refs = append(refs, xtrinode.Spec.ResourceGroupsProfile)
	}
	if jmxConfigMap := jmxExporterConfigMapReference(xtrinode); jmxConfigMap != "" {
		refs = append(refs, jmxConfigMap)
	}
	refs = append(refs, xtrinode.Spec.CustomConfigMaps...)
	refs = append(refs, helmEnvConfigMapReferences(xtrinode.Spec.HelmChartConfig)...)

	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap == nil {
		return refs
	}
	refs = appendOverlayMountReferences(refs, valuesMap["configMounts"], "configMap")
	refs = appendOverlayRoleMountReferences(refs, valuesMap, "configMounts", "configMap")
	refs = appendOverlayEnvValueFromReferences(refs, valuesMap, "configMapKeyRef")
	refs = appendOverlayEnvFromReferences(refs, valuesMap, "configMapRef")
	refs = appendOverlayAdditionalVolumeReferences(refs, valuesMap, "configMap", "name")
	return refs
}

func jmxExporterConfigMapReference(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.KEDA == nil ||
		xtrinode.Spec.KEDA.JMXExporter == nil ||
		!xtrinode.Spec.KEDA.JMXExporter.Enabled {
		return ""
	}
	return xtrinode.Spec.KEDA.JMXExporter.ConfigMap
}

func externalSecretReferences(xtrinode *analyticsv1.XTrinode) []string {
	refs := helmEnvFromSecretReferences(xtrinode.Spec.HelmChartConfig)
	refs = append(refs, authSecretReferences(xtrinode.Spec.GetValuesOverlayMap())...)

	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap == nil {
		return refs
	}
	refs = appendOverlayMountReferences(refs, valuesMap["secretMounts"], "secretName")
	refs = appendOverlayRoleMountReferences(refs, valuesMap, "secretMounts", "secretName")
	refs = appendOverlayEnvValueFromReferences(refs, valuesMap, "secretKeyRef")
	refs = appendOverlayEnvFromReferences(refs, valuesMap, "secretRef")
	refs = appendOverlayAdditionalVolumeReferences(refs, valuesMap, "secret", "secretName")
	refs = appendOverlayAdditionalVolumeReferences(refs, valuesMap, "secret", "name")
	return refs
}

func helmEnvConfigMapReferences(cfg *analyticsv1.HelmChartConfigSpec) []string {
	if cfg == nil {
		return nil
	}
	refs := make([]string, 0)
	for _, envFrom := range cfg.EnvFrom {
		if envFrom.ConfigMapRef != nil && envFrom.ConfigMapRef.Name != "" {
			refs = append(refs, envFrom.ConfigMapRef.Name)
		}
	}
	for _, envVar := range cfg.Env {
		if envVar.ValueFrom != nil && envVar.ValueFrom.ConfigMapKeyRef != nil && envVar.ValueFrom.ConfigMapKeyRef.Name != "" {
			refs = append(refs, envVar.ValueFrom.ConfigMapKeyRef.Name)
		}
	}
	return refs
}

func helmEnvFromSecretReferences(cfg *analyticsv1.HelmChartConfigSpec) []string {
	if cfg == nil {
		return nil
	}
	refs := make([]string, 0)
	for _, envFrom := range cfg.EnvFrom {
		if envFrom.SecretRef != nil && envFrom.SecretRef.Name != "" {
			refs = append(refs, envFrom.SecretRef.Name)
		}
	}
	return refs
}

func authSecretReferences(valuesMap map[string]interface{}) []string {
	if valuesMap == nil {
		return nil
	}
	auth, ok := valuesMap["auth"].(map[string]interface{})
	if !ok {
		return nil
	}
	refs := make([]string, 0, 2)
	for _, key := range []string{"passwordAuthSecret", "groupsAuthSecret"} {
		if secretName, ok := auth[key].(string); ok && secretName != "" {
			refs = append(refs, secretName)
		}
	}
	return refs
}

func appendOverlayRoleMountReferences(refs []string, valuesMap map[string]interface{}, listKey, refKey string) []string {
	for _, role := range []string{"coordinator", "worker"} {
		roleMap, ok := valuesMap[role].(map[string]interface{})
		if !ok {
			continue
		}
		refs = appendOverlayMountReferences(refs, roleMap[listKey], refKey)
	}
	return refs
}

func appendOverlayMountReferences(refs []string, raw interface{}, refKey string) []string {
	items, ok := raw.([]interface{})
	if !ok {
		return refs
	}
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if ref, ok := itemMap[refKey].(string); ok && ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func appendOverlayEnvFromReferences(refs []string, valuesMap map[string]interface{}, refKey string) []string {
	envFromList, ok := valuesMap["envFrom"].([]interface{})
	if !ok {
		return refs
	}
	for _, item := range envFromList {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if ref := nestedString(itemMap, refKey, "name"); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func appendOverlayEnvValueFromReferences(refs []string, valuesMap map[string]interface{}, refKey string) []string {
	envList, ok := valuesMap["env"].([]interface{})
	if !ok {
		return refs
	}
	for _, item := range envList {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		valueFrom, ok := itemMap["valueFrom"].(map[string]interface{})
		if !ok {
			continue
		}
		if ref := nestedString(valueFrom, refKey, "name"); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func appendOverlayAdditionalVolumeReferences(refs []string, valuesMap map[string]interface{}, volumeKey, nameKey string) []string {
	for _, role := range []string{"coordinator", "worker"} {
		roleMap, ok := valuesMap[role].(map[string]interface{})
		if !ok {
			continue
		}
		additionalVolumes, ok := roleMap["additionalVolumes"].([]interface{})
		if !ok {
			continue
		}
		for _, item := range additionalVolumes {
			volumeMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if ref := nestedString(volumeMap, volumeKey, nameKey); ref != "" {
				refs = append(refs, ref)
			}
			refs = appendProjectedVolumeReferences(refs, volumeMap, volumeKey, nameKey)
		}
	}
	return refs
}

func appendProjectedVolumeReferences(refs []string, volumeMap map[string]interface{}, volumeKey, nameKey string) []string {
	projectedMap, ok := volumeMap["projected"].(map[string]interface{})
	if !ok {
		return refs
	}
	sources, ok := projectedMap["sources"].([]interface{})
	if !ok {
		return refs
	}
	for _, source := range sources {
		sourceMap, ok := source.(map[string]interface{})
		if !ok {
			continue
		}
		if ref := nestedString(sourceMap, volumeKey, nameKey); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func nestedString(parent map[string]interface{}, childKey, nameKey string) string {
	childMap, ok := parent[childKey].(map[string]interface{})
	if !ok {
		return ""
	}
	value, ok := childMap[nameKey].(string)
	if !ok {
		return ""
	}
	return value
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func addCatalogSecretDigests(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, d *digest.Digest) (bool, error) {
	// Catalog Secret References (from catalog env var injection)
	// Environment variables from Secrets are resolved when pods start, so Secret
	// value changes require a rollout even when the catalog ConfigMap placeholder
	// itself did not change.
	catalogEnvVars, err := catalog.ExtractCatalogSecretReferences(ctx, cli, xtrinode, logr.Discard())
	if err != nil {
		return false, fmt.Errorf("failed to extract catalog secret references: %w", err)
	}

	sort.Slice(catalogEnvVars, func(i, j int) bool {
		left := catalogEnvVars[i].Name
		right := catalogEnvVars[j].Name
		if left != right {
			return left < right
		}
		leftRef := catalogEnvVars[i].ValueFrom.SecretKeyRef
		rightRef := catalogEnvVars[j].ValueFrom.SecretKeyRef
		if leftRef == nil || rightRef == nil {
			return leftRef != nil
		}
		if leftRef.Name != rightRef.Name {
			return leftRef.Name < rightRef.Name
		}
		return leftRef.Key < rightRef.Key
	})

	hasCatalogSecrets := false
	for _, envVar := range catalogEnvVars {
		if envVar.ValueFrom == nil || envVar.ValueFrom.SecretKeyRef == nil {
			continue
		}
		if err := hashSecretKeyData(ctx, cli, xtrinode.Namespace, envVar.Name, envVar.ValueFrom.SecretKeyRef, d); err != nil {
			return false, err
		}
		hasCatalogSecrets = true
	}
	return hasCatalogSecrets, nil
}

// hashSecretData fetches a secret and adds its data digest to the hasher
// Skips NotFound errors, returns other errors
func hashSecretData(ctx context.Context, cli client.Client, namespace, secretName, prefix string, d *digest.Digest) error {
	secret := &corev1.Secret{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}
	if err := cli.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			// Secret doesn't exist yet - skip it
			return nil
		}
		// Return error for other failures (RBAC, timeout, etc.)
		return fmt.Errorf("failed to fetch secret %s: %w", secretName, err)
	}

	// Hash secret data
	secretDigest := digest.SecretDataDigest(secret)
	d.AddString(prefix, secretDigest)
	return nil
}

func hashConfigMapData(ctx context.Context, cli client.Client, namespace, configMapName, prefix string, d *digest.Digest) error {
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      configMapName,
	}
	if err := cli.Get(ctx, key, configMap); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to fetch configmap %s: %w", configMapName, err)
	}

	configMapDigest := digest.ConfigMapDataDigest(configMap)
	d.AddString(prefix, configMapDigest)
	return nil
}

func hashSecretKeyData(ctx context.Context, cli client.Client, namespace, prefix string, selector *corev1.SecretKeySelector, d *digest.Digest) error {
	d.AddString(prefix+"-secret", selector.Name)
	d.AddString(prefix+"-key", selector.Key)

	secret := &corev1.Secret{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      selector.Name,
	}
	if err := cli.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to fetch catalog secret %s: %w", selector.Name, err)
	}

	if value, ok := secret.Data[selector.Key]; ok {
		d.AddBytes(prefix+"-value", value)
	}
	return nil
}

// StampRolloutHash adds rollout hash annotation to pod template
func StampRolloutHash(template *corev1.PodTemplateSpec, key, hash string) {
	if template.Annotations == nil {
		template.Annotations = map[string]string{}
	}
	template.Annotations[key] = hash
}

// Annotation keys for rollout hashes
const (
	CoordinatorRolloutHashKey = "trino.io/rollout-hash-coordinator"
	WorkerRolloutHashKey      = "trino.io/rollout-hash-worker"
)
