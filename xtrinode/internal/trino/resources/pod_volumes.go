package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func buildVolumes(
	xtrinode *analyticsv1.XTrinode,
	configMapName string,
	catalogs []string,
	role string,
) ([]corev1.Volume, error) {
	volumes := []corev1.Volume{
		{
			Name: "config-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		},
	}

	// Add catalog volume - projected volume merges all catalog ConfigMaps into /etc/trino/catalog/
	// so Trino finds {catalogName}.properties (avoids subPath mount when parent dir doesn't exist)
	if len(catalogs) > 0 {
		sources := make([]corev1.VolumeProjection, 0, len(catalogs))
		for _, catalogName := range catalogs {
			propsFile := fmt.Sprintf("%s.properties", catalogName)
			sources = append(sources, corev1.VolumeProjection{
				ConfigMap: &corev1.ConfigMapProjection{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("trino-catalog-%s", catalogName),
					},
					Items: []corev1.KeyToPath{{
						Key:  propsFile,
						Path: propsFile,
					}},
				},
			})
		}
		volumes = append(volumes, corev1.Volume{
			Name: "catalog-volume",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: sources,
				},
			},
		})
	}

	// Add TLS volumes if enabled
	if xtrinode.Spec.TLS != nil {
		if xtrinode.Spec.TLS.ServerSecretClass != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "server-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: xtrinode.Spec.TLS.ServerSecretClass,
					},
				},
			})
		}
		if xtrinode.Spec.TLS.InternalSecretClass != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "internal-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: xtrinode.Spec.TLS.InternalSecretClass,
					},
				},
			})
		}
	}

	// Add secret mount volumes (global)
	if xtrinode.Spec.HelmChartConfig != nil && len(xtrinode.Spec.HelmChartConfig.SecretMounts) > 0 {
		for _, secretMount := range xtrinode.Spec.HelmChartConfig.SecretMounts {
			volumes = append(volumes, corev1.Volume{
				Name: secretMount.Name,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secretMount.SecretName,
					},
				},
			})
		}
	}

	// Add role-specific secret mount volumes
	addRoleSpecificSecretVolumes(&volumes, xtrinode, role)

	// Add configMount volumes from valuesOverlay (global)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if configMounts, ok := xtrinode.Spec.GetValuesOverlayMap()["configMounts"].([]interface{}); ok {
			for _, cm := range configMounts {
				if cmMap, ok := cm.(map[string]interface{}); ok {
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					name, _ := cmMap["name"].(string)
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					configMap, _ := cmMap["configMap"].(string)
					if name != "" && configMap != "" {
						volumes = append(volumes, corev1.Volume{
							Name: name,
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMap,
									},
								},
							},
						})
					}
				}
			}
		}
		// Add role-specific configMount volumes
		addRoleSpecificConfigVolumes(&volumes, xtrinode, role)
		// Add secretMount volumes from valuesOverlay (global)
		if secretMounts, ok := xtrinode.Spec.GetValuesOverlayMap()["secretMounts"].([]interface{}); ok {
			for _, sm := range secretMounts {
				if smMap, ok := sm.(map[string]interface{}); ok {
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					name, _ := smMap["name"].(string)
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					secretName, _ := smMap["secretName"].(string)
					if name != "" && secretName != "" {
						volumes = append(volumes, corev1.Volume{
							Name: name,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: secretName,
								},
							},
						})
					}
				}
			}
		}
		// Add role-specific secretMount volumes
		addRoleSpecificSecretVolumesFromOverlay(&volumes, xtrinode, role)
	}

	// Add custom ConfigMaps
	if len(xtrinode.Spec.CustomConfigMaps) > 0 {
		for _, cmName := range xtrinode.Spec.CustomConfigMaps {
			volumes = append(volumes, corev1.Volume{
				Name: cmName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cmName,
						},
					},
				},
			})
		}
	}

	// Add resource groups volume if configured (coordinator only, matching upstream chart)
	resourceGroupsTypeConfigMap := false
	if role == "coordinator" && xtrinode.Spec.GetValuesOverlayMap() != nil {
		if resourceGroups, ok := xtrinode.Spec.GetValuesOverlayMap()["resourceGroups"].(map[string]interface{}); ok {
			if rgType, ok := resourceGroups["type"].(string); ok && rgType == "configmap" {
				resourceGroupsTypeConfigMap = true
			}
		}
	}
	if role == "coordinator" && (resourceGroupsTypeConfigMap || xtrinode.Spec.ResourceGroupsProfile != "") {
		configMapName := xtrinode.Spec.ResourceGroupsProfile
		if configMapName == "" {
			configMapName = fmt.Sprintf("trino-%s-resource-groups-volume-coordinator", xtrinode.Name)
		}
		volumes = append(volumes, corev1.Volume{
			Name: "resource-groups-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		})
	}

	// Add access control volume if configured for this role.
	if shouldMountAccessControlVolume(xtrinode, role) {
		accessControlCMName := fmt.Sprintf("trino-%s-access-control-volume-%s", xtrinode.Name, role)
		volumes = append(volumes, corev1.Volume{
			Name: "access-control-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: accessControlCMName,
					},
				},
			},
		})
	}

	// Add JMX exporter config volume if enabled
	if jmxExporterEnabled(xtrinode, role) {
		volumes = append(volumes, corev1.Volume{
			Name: "jmx-exporter-config-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: jmxExporterConfigMapName(xtrinode, role),
					},
				},
			},
		})
	}

	// Add authentication volumes from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		// Password authentication volume
		// Check if passwordAuthSecret is provided OR passwordAuth is provided as string
		passwordAuthSecretName := GetPasswordAuthSecretName(xtrinode)
		if passwordAuthSecretName != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "file-password-authentication-volume",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: passwordAuthSecretName,
						Items: []corev1.KeyToPath{
							{Key: "password.db", Path: "password.db"},
						},
					},
				},
			})
		}
		// Groups authentication volume
		// Check if groupsAuthSecret is provided OR groups is provided as string
		groupsAuthSecretName := GetGroupsAuthSecretName(xtrinode)
		if groupsAuthSecretName != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "file-groups-authentication-volume",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: groupsAuthSecretName,
						Items: []corev1.KeyToPath{
							{Key: "group.db", Path: "group.db"},
						},
					},
				},
			})
		}
	}

	// Add session properties volume from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if sessionProperties, ok := xtrinode.Spec.GetValuesOverlayMap()["sessionProperties"].(map[string]interface{}); ok {
			if sessionType, ok := sessionProperties["type"].(string); ok && (sessionType == "configmap" || sessionType == "properties") {
				volumes = append(volumes, corev1.Volume{
					Name: "session-property-config-volume",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: fmt.Sprintf("trino-%s-session-property-config", xtrinode.Name),
							},
						},
					},
				})
			}
		}
	}

	// Add Kafka schemas volume (always added to match official Helm chart pattern;
	// ConfigMap trino-{name}-schemas-volume-{role} is always created even if empty)
	volumes = append(volumes, corev1.Volume{
		Name: "schemas-volume",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("trino-%s-schemas-volume-%s", xtrinode.Name, role),
				},
			},
		},
	})

	// Add additional volumes from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		switch role {
		case "coordinator":
			if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
				if additionalVolumes, ok := coordinator["additionalVolumes"].([]interface{}); ok {
					for _, vol := range additionalVolumes {
						if volMap, ok := vol.(map[string]interface{}); ok {
							volume, err := buildVolumeFromMap(volMap)
							if err != nil {
								return nil, fmt.Errorf("failed to build volume: %w", err)
							}
							volumes = append(volumes, volume)
						}
					}
				}
			}
		case "worker":
			if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
				if additionalVolumes, ok := worker["additionalVolumes"].([]interface{}); ok {
					for _, vol := range additionalVolumes {
						if volMap, ok := vol.(map[string]interface{}); ok {
							volume, err := buildVolumeFromMap(volMap)
							if err != nil {
								return nil, fmt.Errorf("failed to build volume: %w", err)
							}
							volumes = append(volumes, volume)
						}
					}
				}
			}
		}
	}

	return volumes, nil
}
