package controllers

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsKEDAPlatformUnavailableError(t *testing.T) {
	require.True(t, isKEDAPlatformUnavailableError(&meta.NoKindMatchError{
		GroupKind:        schema.GroupKind{Group: "keda.sh", Kind: "ScaledObject"},
		SearchedVersions: []string{"v1alpha1"},
	}))
	require.True(t, isKEDAPlatformUnavailableError(errors.New(`no matches for kind "ScaledObject" in version "keda.sh/v1alpha1"`)))
	require.True(t, isKEDAPlatformUnavailableError(errors.New(`scaledobjects.keda.sh "runtime" not found`)))
	require.False(t, isKEDAPlatformUnavailableError(errors.New(`deployments.apps "runtime" not found`)))
	require.False(t, isKEDAPlatformUnavailableError(nil))
}
