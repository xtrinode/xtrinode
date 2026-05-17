package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestBuildPasswordAuthSecret(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *corev1.Secret
	}{
		{
			name: "no valuesOverlay returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			wantNil: true,
		},
		{
			name: "passwordAuth as string creates secret",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"passwordAuth": "user1:password1\nuser2:password2",
						},
					}),
				},
			},
			wantNil: false,
			want: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trino-test-trino-password-file",
					Namespace: "default",
					Labels: map[string]string{
						AppNameLabel:      "trino",
						AppInstanceLabel:  "test-trino",
						AppVersionLabel:   "480",
						AppManagedByLabel: ManagedByValue,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "analytics.xtrinode.com/v1",
							Kind:               "XTrinode",
							Name:               "test-trino",
							Controller:         func() *bool { b := true; return &b }(),
							BlockOwnerDeletion: func() *bool { b := true; return &b }(),
						},
					},
				},
				Type: corev1.SecretTypeOpaque,
			},
		},
		{
			name: "passwordAuthSecret provided returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"passwordAuthSecret": "existing-secret",
						},
					}),
				},
			},
			wantNil: true,
		},
		{
			name: "empty passwordAuth returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"passwordAuth": "",
						},
					}),
				},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPasswordAuthSecret(tt.xtrinode)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			assert.Equal(t, tt.want.Name, got.Name)
			assert.Equal(t, tt.want.Namespace, got.Namespace)
			assert.Equal(t, tt.want.Type, got.Type)
			assert.NotNil(t, got.Data)
			assert.Contains(t, got.Data, "password.db")

			// Verify password data (Secret.Data is already raw bytes, not base64 encoded)
			passwordData := got.Data["password.db"]
			if auth, ok := tt.xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{}); ok {
				if passwordAuth, ok := auth["passwordAuth"].(string); ok {
					assert.Equal(t, passwordAuth, string(passwordData))
				}
			}

			// Verify labels and owner references
			assert.Equal(t, tt.want.Labels[AppNameLabel], got.Labels[AppNameLabel])
			assert.Equal(t, tt.want.Labels[AppInstanceLabel], got.Labels[AppInstanceLabel])
			assert.NotNil(t, got.OwnerReferences)
			assert.Len(t, got.OwnerReferences, 1)
		})
	}
}

func TestBuildGroupsAuthSecret(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *corev1.Secret
	}{
		{
			name: "no valuesOverlay returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			wantNil: true,
		},
		{
			name: "groups as string creates secret",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"groups": "admin=admin-group\nuser=user-group",
						},
					}),
				},
			},
			wantNil: false,
			want: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trino-test-trino-groups-file",
					Namespace: "default",
					Labels: map[string]string{
						AppNameLabel:      "trino",
						AppInstanceLabel:  "test-trino",
						AppVersionLabel:   "480",
						AppManagedByLabel: ManagedByValue,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "analytics.xtrinode.com/v1",
							Kind:               "XTrinode",
							Name:               "test-trino",
							Controller:         func() *bool { b := true; return &b }(),
							BlockOwnerDeletion: func() *bool { b := true; return &b }(),
						},
					},
				},
				Type: corev1.SecretTypeOpaque,
			},
		},
		{
			name: "groupsAuthSecret provided returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"groupsAuthSecret": "existing-secret",
						},
					}),
				},
			},
			wantNil: true,
		},
		{
			name: "empty groups returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"groups": "",
						},
					}),
				},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildGroupsAuthSecret(tt.xtrinode)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			assert.Equal(t, tt.want.Name, got.Name)
			assert.Equal(t, tt.want.Namespace, got.Namespace)
			assert.Equal(t, tt.want.Type, got.Type)
			assert.NotNil(t, got.Data)
			assert.Contains(t, got.Data, "group.db")

			// Verify groups data (Secret.Data is already raw bytes, not base64 encoded)
			groupsData := got.Data["group.db"]
			if auth, ok := tt.xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{}); ok {
				if groups, ok := auth["groups"].(string); ok {
					assert.Equal(t, groups, string(groupsData))
				}
			}

			// Verify labels and owner references
			assert.Equal(t, tt.want.Labels[AppNameLabel], got.Labels[AppNameLabel])
			assert.Equal(t, tt.want.Labels[AppInstanceLabel], got.Labels[AppInstanceLabel])
			assert.NotNil(t, got.OwnerReferences)
			assert.Len(t, got.OwnerReferences, 1)
		})
	}
}

func TestGetPasswordAuthSecretName(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		want     string
	}{
		{
			name: "no auth config returns empty string",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			want: "",
		},
		{
			name: "passwordAuthSecret provided returns secret name",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"passwordAuthSecret": "existing-secret",
						},
					}),
				},
			},
			want: "existing-secret",
		},
		{
			name: "passwordAuth as string returns generated name",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"passwordAuth": "user1:password1",
						},
					}),
				},
			},
			want: "trino-test-trino-password-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPasswordAuthSecretName(tt.xtrinode)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPasswordSecretName_CustomShortNameDoesNotPanic(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"auth": map[string]interface{}{
					"passwordAuthSecret": "existing-secret",
				},
			}),
		},
	}

	assert.Equal(t, "existing-secret", passwordSecretName(xtrinode))
}

func TestGetGroupsAuthSecretName(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		want     string
	}{
		{
			name: "no auth config returns empty string",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			want: "",
		},
		{
			name: "groupsAuthSecret provided returns secret name",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"groupsAuthSecret": "existing-secret",
						},
					}),
				},
			},
			want: "existing-secret",
		},
		{
			name: "groups as string returns generated name",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"auth": map[string]interface{}{
							"groups": "admin=admin-group",
						},
					}),
				},
			},
			want: "trino-test-trino-groups-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetGroupsAuthSecretName(tt.xtrinode)
			assert.Equal(t, tt.want, got)
		})
	}
}
