package resources

import (
	"fmt"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/trino/controlauth"
	corev1 "k8s.io/api/core/v1"
)

func appendTrinoControlAuthEnv(env []corev1.EnvVar, xtrinode *analyticsv1.XTrinode, role string) []corev1.EnvVar {
	if role != "worker" || !controlauth.HasPasswordSecret(xtrinode) {
		return env
	}

	secretRef := *xtrinode.Spec.TrinoControlAuth.PasswordSecret
	return append(env,
		corev1.EnvVar{
			Name:  controlauth.EnvUsername,
			Value: controlauth.Username(xtrinode),
		},
		corev1.EnvVar{
			Name: controlauth.EnvPassword,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &secretRef,
			},
		},
	)
}

func workerGracefulShutdownCommand(xtrinode *analyticsv1.XTrinode, gracePeriodSeconds int64) string {
	authArgs := ""
	forwardedProtoHeader := ""
	userHeader := controlauth.Username(xtrinode)
	if controlauth.HasPasswordSecret(xtrinode) {
		authArgs = fmt.Sprintf("-u \"${%s}:${%s}\" ", controlauth.EnvUsername, controlauth.EnvPassword)
		forwardedProtoHeader = fmt.Sprintf("-H '%s: %s' ", controlauth.ForwardedProtoHeader, controlauth.ForwardedProtoHTTPS)
		userHeader = fmt.Sprintf("${%s}", controlauth.EnvUsername)
	}
	if userHeader == "" {
		userHeader = config.TrinoOperatorUser
	}

	return fmt.Sprintf(
		"status=0; curl --max-time 5 -v -X PUT %s-d '\"SHUTTING_DOWN\"' -H 'Content-type: application/json' %s-H \"X-Trino-User: %s\" http://localhost:%d/v1/info/state || status=$?; sleep %d; exit $status",
		authArgs,
		forwardedProtoHeader,
		userHeader,
		trinoHTTPPort(xtrinode),
		gracePeriodSeconds,
	)
}

func trinoControlUsername(xtrinode *analyticsv1.XTrinode) string {
	return controlauth.Username(xtrinode)
}
