package resources

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/digest"
	"github.com/xtrinode/xtrinode/internal/sizing"
	"github.com/xtrinode/xtrinode/internal/trino/controlauth"
)

// BuildCoordinatorConfigMap builds the coordinator ConfigMap with revisioned name
func BuildCoordinatorConfigMap(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	catalogs []string,
	revision string,
) (*corev1.ConfigMap, error) {
	data := buildCoordinatorConfigMapData(xtrinode, preset, catalogs)
	return buildCoordinatorConfigMapFromData(xtrinode, data, revision, revision), nil
}

func buildCoordinatorConfigMapData(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	catalogs []string,
) map[string]string {
	data := map[string]string{
		"node.properties":   buildNodeProperties(xtrinode),
		"jvm.config":        buildJVMConfig(xtrinode, preset, "coordinator"),
		"config.properties": buildConfigProperties(xtrinode, preset, "coordinator", catalogs),
		"log.properties":    buildLogProperties(xtrinode),
	}

	// Add access control if configured
	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.AccessControl != nil {
		switch xtrinode.Spec.HelmChartConfig.AccessControl.Type {
		case "configmap":
			data["access-control.properties"] = buildAccessControlProperties(xtrinode)
		case "properties":
			data["access-control.properties"] = xtrinode.Spec.HelmChartConfig.AccessControl.Properties
		}
	}

	// Add resource groups if configured. Upstream wires resource groups only on the coordinator.
	if xtrinode.Spec.ResourceGroupsProfile != "" {
		data["resource-groups.properties"] = buildResourceGroupsProperties(xtrinode)
	} else if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if resourceGroups, ok := xtrinode.Spec.GetValuesOverlayMap()["resourceGroups"].(map[string]interface{}); ok {
			if rgType, ok := resourceGroups["type"].(string); ok {
				switch rgType {
				case "configmap":
					data["resource-groups.properties"] = buildResourceGroupsProperties(xtrinode)
				case "properties":
					if properties, ok := resourceGroups["properties"].(string); ok && properties != "" {
						data["resource-groups.properties"] = properties
					}
				}
			}
		}
	}

	// Add exchange manager if fault tolerant execution is enabled
	if faultTolerantEnabled(xtrinode) && faultTolerantExchangeManagerEnabled(xtrinode) {
		data["exchange-manager.properties"] = buildExchangeManagerProperties(xtrinode)
	}

	// Add event listener properties from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if eventListenerProps, ok := xtrinode.Spec.GetValuesOverlayMap()["eventListenerProperties"].([]interface{}); ok && len(eventListenerProps) > 0 {
			eventListenerLines := []string{}
			for _, prop := range eventListenerProps {
				if propStr, ok := prop.(string); ok {
					eventListenerLines = append(eventListenerLines, propStr)
				}
			}
			if len(eventListenerLines) > 0 {
				data["event-listener.properties"] = strings.Join(eventListenerLines, "\n")
			}
		}
	}

	// Add additional config files from HelmChartConfig
	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.Coordinator != nil {
		for fileName, fileContent := range xtrinode.Spec.HelmChartConfig.Coordinator.AdditionalConfigFiles {
			data[fileName] = fileContent
		}
	}

	// Add authentication and session properties config files from valuesOverlay
	addAuthenticationConfigFiles(xtrinode, data)
	addSessionPropertiesConfigFiles(xtrinode, data)

	return data
}

func buildCoordinatorConfigMapFromData(
	xtrinode *analyticsv1.XTrinode,
	data map[string]string,
	nameRevision string,
	resourceRevision string,
) *corev1.ConfigMap {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            coordinatorConfigMapName(xtrinode, nameRevision),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: data,
	}

	// Stamp revision on ConfigMap
	StampRevision(configMap, resourceRevision)

	return configMap
}

// BuildWorkerConfigMap builds the worker ConfigMap with revisioned name
func BuildWorkerConfigMap(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	catalogs []string,
	revision string,
) (*corev1.ConfigMap, error) {
	data := buildWorkerConfigMapData(xtrinode, preset, catalogs)
	return buildWorkerConfigMapFromData(xtrinode, data, revision, revision), nil
}

func buildWorkerConfigMapData(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	catalogs []string,
) map[string]string {
	data := map[string]string{
		"node.properties":   buildNodeProperties(xtrinode),
		"jvm.config":        buildJVMConfig(xtrinode, preset, "worker"),
		"config.properties": buildConfigProperties(xtrinode, preset, "worker", catalogs),
		"log.properties":    buildLogProperties(xtrinode),
	}

	// Add worker-local access control for graceful shutdown.
	gracefulShutdownEnabled, _ := workerGracefulShutdownSettings(xtrinode)
	if gracefulShutdownEnabled {
		data["access-control.properties"] = buildWorkerGracefulShutdownAccessControlProperties()
	}

	// Add exchange manager if fault tolerant execution is enabled
	if faultTolerantEnabled(xtrinode) && faultTolerantExchangeManagerEnabled(xtrinode) {
		data["exchange-manager.properties"] = buildExchangeManagerProperties(xtrinode)
	}

	// Add event listener properties from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if eventListenerProps, ok := xtrinode.Spec.GetValuesOverlayMap()["eventListenerProperties"].([]interface{}); ok && len(eventListenerProps) > 0 {
			eventListenerLines := []string{}
			for _, prop := range eventListenerProps {
				if propStr, ok := prop.(string); ok {
					eventListenerLines = append(eventListenerLines, propStr)
				}
			}
			if len(eventListenerLines) > 0 {
				data["event-listener.properties"] = strings.Join(eventListenerLines, "\n")
			}
		}
	}

	// Workers also expose HTTP lifecycle APIs. When Trino HTTP auth is enabled,
	// they need the same authenticator config as coordinators for preStop calls.
	addAuthenticationConfigFiles(xtrinode, data)

	return data
}

func buildWorkerConfigMapFromData(
	xtrinode *analyticsv1.XTrinode,
	data map[string]string,
	nameRevision string,
	resourceRevision string,
) *corev1.ConfigMap {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            workerConfigMapName(xtrinode, nameRevision),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: data,
	}

	// Stamp revision on ConfigMap
	StampRevision(configMap, resourceRevision)

	return configMap
}

func configMapDataRevision(data map[string]string) string {
	d := digest.New()
	d.AddJSON("data", data)
	return d.Sum12()
}

// Helper functions

func coordinatorConfigMapName(xtrinode *analyticsv1.XTrinode, revision string) string {
	// Revisioned name: trino-{name}-coordinator-{revision}
	return fmt.Sprintf("%s-coordinator-%s", config.BuildCoordinatorServiceName(xtrinode.Name), revision)
}

func workerConfigMapName(xtrinode *analyticsv1.XTrinode, revision string) string {
	// Revisioned name: trino-{name}-worker-{revision}
	return fmt.Sprintf("%s-worker-%s", config.BuildWorkerServiceName(xtrinode.Name), revision)
}

func buildNodeProperties(xtrinode *analyticsv1.XTrinode) string {
	environment := "production"
	// Environment can be overridden via valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{}); ok {
			if node, ok := server["node"].(map[string]interface{}); ok {
				if env, ok := node["environment"].(string); ok {
					environment = env
				}
			}
		}
	}

	props := []string{
		fmt.Sprintf("node.environment=%s", environment),
		"node.data-dir=/data/trino",
		"plugin.dir=/usr/lib/trino/plugin",
	}

	// Add additional node properties from valuesOverlay if present
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if additionalProps, ok := xtrinode.Spec.GetValuesOverlayMap()["additionalNodeProperties"].([]interface{}); ok {
			for _, prop := range additionalProps {
				if propStr, ok := prop.(string); ok {
					props = append(props, propStr)
				}
			}
		}
	}

	return strings.Join(props, "\n")
}

func buildJVMConfig(xtrinode *analyticsv1.XTrinode, preset *sizing.SizePreset, role string) string {
	var maxHeapSize string
	if role == "coordinator" {
		maxHeapSize = preset.CoordinatorMemLim
	} else {
		maxHeapSize = preset.WorkerMemLim
	}

	// Convert memory limit to JVM format (e.g., "8Gi" -> "8G")
	maxHeapSize = strings.ReplaceAll(maxHeapSize, "Gi", "G")
	maxHeapSize = strings.ReplaceAll(maxHeapSize, "Mi", "M")

	jvmOpts := []string{
		"-server",
		"-agentpath:/usr/lib/trino/bin/libjvmkill.so",
		fmt.Sprintf("-Xmx%s", maxHeapSize),
		"-XX:+UseG1GC",
		"-XX:G1HeapRegionSize=32M",
		"-XX:+ExplicitGCInvokesConcurrent",
		"-XX:+HeapDumpOnOutOfMemoryError",
		"-XX:+ExitOnOutOfMemoryError",
		"-XX:-OmitStackTraceInFastThrow",
		"-XX:ReservedCodeCacheSize=512M",
		"-XX:PerMethodRecompilationCutoff=10000",
		"-XX:PerBytecodeRecompilationCutoff=10000",
		"-Djdk.attach.allowAttachSelf=true",
		"-Djdk.nio.maxCachedBufferSize=2000000",
		"-XX:+EnableDynamicAgentLoading",
	}

	// Add version-specific JVM flags for Trino 447+.
	// https://bugs.openjdk.org/browse/JDK-8329528
	image := map[string]interface{}{}
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if overlayImage, ok := xtrinode.Spec.GetValuesOverlayMap()["image"].(map[string]interface{}); ok {
			image = overlayImage
		}
	}

	// Only check version if using default trinodb/trino image without digest/registry override.
	repository := config.DefaultTrinoImageRepository
	if repo, ok := image["repository"].(string); ok {
		repository = repo
	}
	useRepositoryAsSoleImageReference := false
	if useSole, ok := image["useRepositoryAsSoleImageReference"].(bool); ok {
		useRepositoryAsSoleImageReference = useSole
	}
	registry := ""
	if reg, ok := image["registry"].(string); ok {
		registry = reg
	}
	imageDigest := ""
	if dig, ok := image["digest"].(string); ok {
		imageDigest = dig
	}

	if repository == config.DefaultTrinoImageRepository && !useRepositoryAsSoleImageReference && registry == "" && imageDigest == "" {
		tag := config.DefaultTrinoImageTag
		if overlayTag, ok := image["tag"].(string); ok && overlayTag != "" {
			tag = overlayTag
		}
		var version int
		if _, err := fmt.Sscanf(tag, "%d", &version); err == nil && version > 447 {
			jvmOpts = append(jvmOpts,
				"-XX:+UnlockDiagnosticVMOptions",
				"-XX:G1NumCollectionsKeepPinned=10000000",
			)
		}
	}

	// Add JMX config if enabled
	if jmxEnabled(xtrinode, role) {
		jvmOpts = append(jvmOpts,
			fmt.Sprintf("-Dcom.sun.management.jmxremote.rmi.port=%d", jmxServerPort(xtrinode, role)),
		)
	}

	// Add additional JVM config from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if role == "coordinator" {
			if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
				if additionalJVM, ok := coordinator["additionalJVMConfig"].([]interface{}); ok {
					for _, opt := range additionalJVM {
						if optStr, ok := opt.(string); ok {
							jvmOpts = append(jvmOpts, optStr)
						}
					}
				}
			}
		} else {
			if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
				if additionalJVM, ok := worker["additionalJVMConfig"].([]interface{}); ok {
					for _, opt := range additionalJVM {
						if optStr, ok := opt.(string); ok {
							jvmOpts = append(jvmOpts, optStr)
						}
					}
				}
			}
		}
	}

	return strings.Join(jvmOpts, "\n")
}

// safeQueryMemoryPerNode returns ~50% of memLim to leave heap headroom for Trino.
// Trino requires: query.max-memory-per-node + heap_headroom <= heap; headroom ~30% of query mem.
func safeQueryMemoryPerNode(memLim string) string {
	// Parse "8Gi" -> 8, "32Gi" -> 32, "4Gi" -> 4, "512Mi" -> 0.5
	var n int
	var unit string
	var err error
	if strings.HasSuffix(memLim, "Gi") {
		unit = "Gi"
		n, err = strconv.Atoi(strings.TrimSuffix(memLim, "Gi"))
	} else if strings.HasSuffix(memLim, "Mi") {
		unit = "Mi"
		n, err = strconv.Atoi(strings.TrimSuffix(memLim, "Mi"))
	} else {
		return memLim // fallback
	}
	if err != nil {
		return memLim
	}
	half := n / 2
	if half < 1 {
		half = 1
	}
	return fmt.Sprintf("%d%s", half, unit)
}

func buildConfigProperties(xtrinode *analyticsv1.XTrinode, preset *sizing.SizePreset, role string, catalogs []string) string {
	props := []string{}

	// Coordinator vs worker
	if role == "coordinator" {
		props = append(props, "coordinator=true")
	} else {
		props = append(props, "coordinator=false")
	}

	if role == "coordinator" {
		includeCoordinator := false
		if coordinator := roleValuesOverlay(xtrinode, "coordinator"); coordinator != nil {
			if cfg, ok := coordinator["config"].(map[string]interface{}); ok {
				if nodeScheduler, ok := cfg["nodeScheduler"].(map[string]interface{}); ok {
					if include, ok := nodeScheduler["includeCoordinator"].(bool); ok {
						includeCoordinator = include
					}
				}
			}
		}
		props = append(props, fmt.Sprintf("node-scheduler.include-coordinator=%v", includeCoordinator))
	}
	httpPort := trinoHTTPPort(xtrinode)
	props = append(props, fmt.Sprintf("http-server.http.port=%d", httpPort))
	if shouldProcessForwardedHeaders(xtrinode) {
		props = append(props, "http-server.process-forwarded=true")
	}

	// Query memory limits
	queryMaxMemory := "4GB"
	if xtrinode.Spec.Limits != nil && xtrinode.Spec.Limits.Session != nil && xtrinode.Spec.Limits.Session.MaxQueryMemory != "" {
		queryMaxMemory = xtrinode.Spec.Limits.Session.MaxQueryMemory
	}
	props = append(props, fmt.Sprintf("query.max-memory=%s", queryMaxMemory))

	// Max memory per node - use ~50% of limit to leave heap headroom for Trino
	// (query.max-memory-per-node + heap_headroom must be <= heap; headroom ~30% of query mem)
	var memLim string
	if role == "coordinator" {
		memLim = preset.CoordinatorMemLim
	} else {
		memLim = preset.WorkerMemLim
	}
	maxMemoryPerNode := safeQueryMemoryPerNode(memLim)
	// Convert to format Trino expects (remove 'i' from Gi/Mi)
	maxMemoryPerNode = strings.ReplaceAll(maxMemoryPerNode, "Gi", "GB")
	maxMemoryPerNode = strings.ReplaceAll(maxMemoryPerNode, "Mi", "MB")
	props = append(props, fmt.Sprintf("query.max-memory-per-node=%s", maxMemoryPerNode))

	// Discovery URI
	if role == "coordinator" {
		props = append(props, fmt.Sprintf("discovery.uri=http://localhost:%d", httpPort))
	} else {
		coordinatorService := coordinatorServiceName(xtrinode)
		props = append(props, fmt.Sprintf("discovery.uri=http://%s:%d", coordinatorService, httpPort))
	}

	// Authentication type (Helm chart: server.config.authenticationType)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{}); ok {
			if cfg, ok := server["config"].(map[string]interface{}); ok {
				if authType, ok := cfg["authenticationType"].(string); ok && authType != "" {
					props = append(props, fmt.Sprintf("http-server.authentication.type=%s", authType))
				}
			}
		}
	}

	// TLS configuration
	// When TLS is enabled, disable HTTP to enforce TLS-only.
	if xtrinode.Spec.TLS != nil && xtrinode.Spec.TLS.ServerSecretClass != "" {
		// Disable HTTP when TLS is enabled (TLS-only mode)
		props = append(props,
			"http-server.https.enabled=true",
			fmt.Sprintf("http-server.https.port=%d", config.TrinoPortHTTPS),
			"http-server.https.keystore.path=/etc/trino/tls/server/keystore.p12",
			"http-server.https.keystore.key=keystore",
			"http-server.http.enabled=false",
		)
	}

	// Internal TLS
	if xtrinode.Spec.TLS != nil && xtrinode.Spec.TLS.InternalSecretClass != "" {
		props = append(props,
			"internal-communication.https.enabled=true",
			"internal-communication.https.keystore.path=/etc/trino/tls/internal/keystore.p12",
			"internal-communication.https.keystore.key=keystore",
			"internal-communication.https.truststore.path=/etc/trino/tls/internal/truststore.p12",
			"internal-communication.https.truststore.key=truststore",
		)
	}

	gracefulShutdownEnabled, gracePeriodSeconds := workerGracefulShutdownSettings(xtrinode)
	if gracefulShutdownEnabled {
		props = append(props, fmt.Sprintf("shutdown.grace-period=%ds", gracePeriodSeconds))
	}

	// Fault tolerant execution
	if faultTolerantEnabled(xtrinode) {
		props = append(props, fmt.Sprintf("retry-policy=%s", faultTolerantRetryPolicy(xtrinode)))
	}

	// JMX configuration
	if jmxEnabled(xtrinode, role) {
		props = append(props,
			fmt.Sprintf("jmx.rmiregistry.port=%d", jmxRegistryPort(xtrinode, role)),
			fmt.Sprintf("jmx.rmiserver.port=%d", jmxServerPort(xtrinode, role)),
		)
	}

	// Additional config properties from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if additionalProps, ok := xtrinode.Spec.GetValuesOverlayMap()["additionalConfigProperties"].([]interface{}); ok {
			for _, prop := range additionalProps {
				if propStr, ok := prop.(string); ok {
					props = append(props, propStr)
				}
			}
		}
	}

	// Coordinator extra config
	if role == "coordinator" && xtrinode.Spec.GetValuesOverlayMap() != nil {
		if server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{}); ok {
			if coordinatorExtraConfig, ok := server["coordinatorExtraConfig"].(string); ok && coordinatorExtraConfig != "" {
				// Split by newlines and append each line
				lines := strings.Split(coordinatorExtraConfig, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line != "" && !strings.HasPrefix(line, "#") {
						props = append(props, line)
					}
				}
			}
		}
	}

	// Worker extra config
	if role == "worker" && xtrinode.Spec.GetValuesOverlayMap() != nil {
		if server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{}); ok {
			if workerExtraConfig, ok := server["workerExtraConfig"].(string); ok && workerExtraConfig != "" {
				// Split by newlines and append each line
				lines := strings.Split(workerExtraConfig, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line != "" && !strings.HasPrefix(line, "#") {
						props = append(props, line)
					}
				}
			}
		}
	}

	return strings.Join(props, "\n")
}

// BuildSessionPropertyConfigMap builds the session properties ConfigMap
func BuildSessionPropertyConfigMap(xtrinode *analyticsv1.XTrinode) *corev1.ConfigMap {
	data := make(map[string]string)

	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if sessionProperties, ok := xtrinode.Spec.GetValuesOverlayMap()["sessionProperties"].(map[string]interface{}); ok {
			if sessionType, ok := sessionProperties["type"].(string); ok {
				switch sessionType {
				case "configmap":
					if sessionPropertiesConfig, ok := sessionProperties["sessionPropertiesConfig"].(string); ok {
						data["session-property-config.properties"] = "session-property-config.configuration-manager=file\nsession-property-manager.config-file=/etc/trino/session-property-config.json"
						data["session-property-config.json"] = sessionPropertiesConfig
					}
				case "properties":
					if properties, ok := sessionProperties["properties"].(string); ok {
						data["session-property-config.properties"] = properties
					}
				}
			}
		}
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("trino-%s-session-property-config", xtrinode.Name),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: data,
	}
}

func shouldProcessForwardedHeaders(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode == nil {
		return false
	}
	return xtrinode.Spec.Routing != nil ||
		controlauth.HasPasswordSecret(xtrinode) ||
		controlauth.HTTPAuthenticationConfigured(xtrinode)
}

// BuildKafkaSchemasConfigMap builds the Kafka schemas ConfigMap
// getRoleSpecificJMXConfig gets role-specific JMX configuration
func getRoleSpecificJMXConfig(xtrinode *analyticsv1.XTrinode, role string) map[string]interface{} {
	return roleJMXValues(xtrinode, role)
}

// addAuthenticationConfigFiles adds authentication config files from valuesOverlay
func addAuthenticationConfigFiles(xtrinode *analyticsv1.XTrinode, data map[string]string) {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return
	}

	auth, ok := xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{})
	if !ok {
		return
	}

	// Password authentication config
	passwordAuthSecretName := GetPasswordAuthSecretName(xtrinode)
	if passwordAuthSecretName != "" {
		if isPasswordAuthTypeEnabled(xtrinode) {
			if _, exists := data["password-authenticator.properties"]; !exists {
				data["password-authenticator.properties"] = "password-authenticator.name=file\nfile.password-file=/etc/trino/auth/password/password.db"
			}
		}
	}

	// Groups authentication config
	if isGroupsAuthEnabled(auth) {
		if _, exists := data["group-provider.properties"]; !exists {
			refreshPeriod := "5s"
			if refreshPeriodVal, ok := auth["refreshPeriod"].(string); ok && refreshPeriodVal != "" {
				refreshPeriod = refreshPeriodVal
			}
			data["group-provider.properties"] = "group-provider.name=file\nfile.group-file=/etc/trino/auth/group/group.db\nfile.refresh-period=" + refreshPeriod
		}
	}
}

// isPasswordAuthTypeEnabled checks if PASSWORD authentication type is configured
func isPasswordAuthTypeEnabled(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return false
	}

	server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{})
	if !ok {
		return false
	}

	cfg, ok := server["config"].(map[string]interface{})
	if !ok {
		return false
	}

	authType, ok := cfg["authenticationType"].(string)
	return ok && strings.Contains(authType, "PASSWORD")
}

// isGroupsAuthEnabled checks if groups authentication is enabled
func isGroupsAuthEnabled(auth map[string]interface{}) bool {
	if groups, ok := auth["groups"].(string); ok && groups != "" {
		return true
	}
	if groupsAuthSecret, ok := auth["groupsAuthSecret"].(string); ok && groupsAuthSecret != "" {
		return true
	}
	return false
}

// addSessionPropertiesConfigFiles adds session properties config from valuesOverlay
func addSessionPropertiesConfigFiles(xtrinode *analyticsv1.XTrinode, data map[string]string) {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return
	}

	sessionProperties, ok := xtrinode.Spec.GetValuesOverlayMap()["sessionProperties"].(map[string]interface{})
	if !ok {
		return
	}

	sessionType, ok := sessionProperties["type"].(string)
	if !ok {
		return
	}

	switch sessionType {
	case "configmap":
		if sessionPropertiesConfig, ok := sessionProperties["sessionPropertiesConfig"].(string); ok {
			data["session-property-config.properties"] = "session-property-config.configuration-manager=file\nsession-property-manager.config-file=/etc/trino/session-property-config.json"
			data["session-property-config.json"] = sessionPropertiesConfig
		}
	case "properties":
		if properties, ok := sessionProperties["properties"].(string); ok {
			data["session-property-config.properties"] = properties
		}
	}
}

func BuildKafkaSchemasConfigMap(xtrinode *analyticsv1.XTrinode, role string) *corev1.ConfigMap {
	data := make(map[string]string)

	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if kafka, ok := xtrinode.Spec.GetValuesOverlayMap()["kafka"].(map[string]interface{}); ok {
			if tableDescriptions, ok := kafka["tableDescriptions"].(map[string]interface{}); ok {
				for key, val := range tableDescriptions {
					if valStr, ok := val.(string); ok {
						data[key] = valStr
					}
				}
			}
		}
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("trino-%s-schemas-volume-%s", xtrinode.Name, role),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: data,
	}
}

// BuildJMXExporterConfigMap builds the JMX exporter configuration ConfigMap.
// Returns nil if JMX exporter is not enabled or an external config ConfigMap is configured.
func BuildJMXExporterConfigMap(xtrinode *analyticsv1.XTrinode, role string) *corev1.ConfigMap {
	var configProperties string
	externalConfigMap := false

	// Get role-specific JMX config (coordinator or worker)
	roleJmxConfig := getRoleSpecificJMXConfig(xtrinode, role)

	if roleJmxConfig != nil {
		if exporter, ok := roleJmxConfig["exporter"].(map[string]interface{}); ok {
			if configProps, ok := exporter["configProperties"].(string); ok && configProps != "" {
				configProperties = configProps
			}
		}
	}

	if xtrinode.Spec.KEDA != nil &&
		xtrinode.Spec.KEDA.JMXExporter != nil &&
		xtrinode.Spec.KEDA.JMXExporter.ConfigMap != "" {
		externalConfigMap = true
	}

	if !jmxExporterEnabled(xtrinode, role) || externalConfigMap {
		return nil
	}

	// Build ConfigMap data
	data := make(map[string]string)
	if configProperties != "" {
		data["jmx-exporter-config.yaml"] = configProperties
	} else {
		// Default minimal config if not provided
		data["jmx-exporter-config.yaml"] = fmt.Sprintf("hostPort: localhost:%d\nstartDelaySeconds: 0\nssl: false", jmxRegistryPort(xtrinode, role))
	}

	labels := TrinoLabels(xtrinode)
	labels[AppComponentLabel] = "jmx"

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            jmxExporterConfigMapName(xtrinode, role),
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: data,
	}
}

func buildLogProperties(xtrinode *analyticsv1.XTrinode) string {
	logLevel := "INFO"
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{}); ok {
			if log, ok := server["log"].(map[string]interface{}); ok {
				if trino, ok := log["trino"].(map[string]interface{}); ok {
					if level, ok := trino["level"].(string); ok {
						logLevel = level
					}
				}
			}
		}
	}

	props := []string{
		fmt.Sprintf("io.trino=%s", logLevel),
	}

	// Add additional log properties from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if additionalLogProps, ok := xtrinode.Spec.GetValuesOverlayMap()["additionalLogProperties"].([]interface{}); ok {
			for _, prop := range additionalLogProps {
				if propStr, ok := prop.(string); ok {
					props = append(props, propStr)
				}
			}
		}
	}

	return strings.Join(props, "\n")
}

func buildAccessControlProperties(xtrinode *analyticsv1.XTrinode) string {
	props := []string{
		"access-control.name=file",
	}

	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.AccessControl != nil {
		if xtrinode.Spec.HelmChartConfig.AccessControl.RefreshPeriod != "" {
			props = append(props, fmt.Sprintf("security.refresh-period=%s", xtrinode.Spec.HelmChartConfig.AccessControl.RefreshPeriod))
		}
		configFile := "rules.json"
		if xtrinode.Spec.HelmChartConfig.AccessControl.ConfigFile != "" {
			configFile = xtrinode.Spec.HelmChartConfig.AccessControl.ConfigFile
		}
		props = append(props, fmt.Sprintf("security.config-file=/etc/trino/access-control/%s", configFile))
	}

	return strings.Join(props, "\n")
}

func buildWorkerGracefulShutdownAccessControlProperties() string {
	return "access-control.name=file" + "\n" +
		"security.config-file=/etc/trino/access-control/graceful-shutdown-rules.json"
}

func buildResourceGroupsProperties(xtrinode *analyticsv1.XTrinode) string {
	return "resource-groups.configuration-manager=file" + "\n" + "resource-groups.config-file=/etc/trino/resource-groups/resource-groups.json"
}

func buildExchangeManagerProperties(xtrinode *analyticsv1.XTrinode) string {
	var exchangeManager *analyticsv1.ExchangeManagerSpec
	if xtrinode.Spec.FaultTolerantExecution != nil {
		exchangeManager = xtrinode.Spec.FaultTolerantExecution.ExchangeManager
	}

	name := "filesystem"
	if exchangeManager != nil && strings.TrimSpace(exchangeManager.Name) != "" {
		name = strings.TrimSpace(exchangeManager.Name)
	}

	props := []string{
		fmt.Sprintf("exchange-manager.name=%s", name),
	}

	baseDirectories := []string{}
	if exchangeManager != nil && len(exchangeManager.BaseDirectories) > 0 {
		baseDirectories = append(baseDirectories, exchangeManager.BaseDirectories...)
	} else if name == "filesystem" {
		baseDirectories = append(baseDirectories, "/tmp/trino-exchange")
	}
	if len(baseDirectories) > 0 {
		props = append(props, fmt.Sprintf("exchange.base-directories=%s", strings.Join(baseDirectories, ",")))
	}

	if exchangeManager != nil && len(exchangeManager.Properties) > 0 {
		keys := make([]string, 0, len(exchangeManager.Properties))
		for key := range exchangeManager.Properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			props = append(props, fmt.Sprintf("%s=%s", key, exchangeManager.Properties[key]))
		}
	}

	// Add additional exchange manager properties from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if additionalProps, ok := xtrinode.Spec.GetValuesOverlayMap()["additionalExchangeManagerProperties"].([]interface{}); ok {
			for _, prop := range additionalProps {
				if propStr, ok := prop.(string); ok {
					props = append(props, propStr)
				}
			}
		}
	}

	return strings.Join(props, "\n")
}

func faultTolerantEnabled(xtrinode *analyticsv1.XTrinode) bool {
	return xtrinode.Spec.FaultTolerantExecution != nil
}

func faultTolerantExchangeManagerEnabled(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.FaultTolerantExecution == nil ||
		xtrinode.Spec.FaultTolerantExecution.ExchangeManager == nil ||
		xtrinode.Spec.FaultTolerantExecution.ExchangeManager.Enabled == nil {
		return true
	}
	return *xtrinode.Spec.FaultTolerantExecution.ExchangeManager.Enabled
}

func faultTolerantRetryPolicy(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.FaultTolerantExecution != nil {
		policy := strings.ToUpper(strings.TrimSpace(xtrinode.Spec.FaultTolerantExecution.RetryPolicy))
		if policy != "" {
			return policy
		}
	}
	return "TASK"
}

// BuildAccessControlConfigMapCoordinator builds the access control ConfigMap for coordinator
// Helm chart: configmap-access-control-coordinator.yaml
func BuildAccessControlConfigMapCoordinator(xtrinode *analyticsv1.XTrinode) *corev1.ConfigMap {
	if xtrinode.Spec.HelmChartConfig == nil || xtrinode.Spec.HelmChartConfig.AccessControl == nil {
		return nil
	}
	if xtrinode.Spec.HelmChartConfig.AccessControl.Type != "configmap" {
		return nil
	}

	data := make(map[string]string)
	// Add rules from accessControl.rules
	if xtrinode.Spec.HelmChartConfig.AccessControl.Rules != nil {
		for key, val := range xtrinode.Spec.HelmChartConfig.AccessControl.Rules {
			data[key] = val
		}
	}

	labels := TrinoLabels(xtrinode)
	labels[AppComponentLabel] = "coordinator"

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("trino-%s-access-control-volume-coordinator", xtrinode.Name),
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: data,
	}
}

// BuildAccessControlConfigMapWorker builds the access control ConfigMap for worker graceful shutdown
// Helm chart: configmap-access-control-worker.yaml (only created when gracefulShutdown.enabled)
func BuildAccessControlConfigMapWorker(xtrinode *analyticsv1.XTrinode) *corev1.ConfigMap {
	gracefulShutdownEnabled, _ := workerGracefulShutdownSettings(xtrinode)
	if !gracefulShutdownEnabled {
		return nil
	}

	// Build graceful shutdown access control rules (Helm chart pattern)
	user := strconv.Quote(trinoControlUsername(xtrinode))
	gracefulShutdownRules := fmt.Sprintf(`{
  "system_information": [
    {
      "allow": [
        "write"
      ],
      "user": %s
    }
  ]
}`, user)

	labels := TrinoLabels(xtrinode)
	labels[AppComponentLabel] = "worker"

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("trino-%s-access-control-volume-worker", xtrinode.Name),
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: map[string]string{
			"graceful-shutdown-rules.json": gracefulShutdownRules,
		},
	}
}
