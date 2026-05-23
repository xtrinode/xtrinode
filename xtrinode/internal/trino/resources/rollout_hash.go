package resources

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/digest"
	"github.com/xtrinode/xtrinode/internal/rollout"
)

type roleRolloutDigests struct {
	Catalog       string
	AccessControl string
	SessionProps  string
	Secret        string
}

func coordinatorPodRolloutHash(template *corev1.PodTemplateSpec, digests roleRolloutDigests) string {
	return podRolloutHash(template, rollout.CoordinatorRolloutHashKey, true, digests)
}

func workerPodRolloutHash(template *corev1.PodTemplateSpec, includeCatalog bool, digests roleRolloutDigests) string {
	return podRolloutHash(template, rollout.WorkerRolloutHashKey, includeCatalog, digests)
}

func podRolloutHash(
	template *corev1.PodTemplateSpec,
	rolloutHashKey string,
	includeCatalog bool,
	rolloutDigests roleRolloutDigests,
) string {
	renderedTemplate := template.DeepCopy()
	if renderedTemplate.Annotations != nil {
		delete(renderedTemplate.Annotations, config.RevisionAnnotationKey)
		delete(renderedTemplate.Annotations, rollout.CoordinatorRolloutHashKey)
		delete(renderedTemplate.Annotations, rollout.WorkerRolloutHashKey)
		if len(renderedTemplate.Annotations) == 0 {
			renderedTemplate.Annotations = nil
		}
	}

	d := digest.New()
	d.AddString("rolloutHashKey", rolloutHashKey)
	d.AddJSON("podTemplate", renderedTemplate)
	d.AddString("access", rolloutDigests.AccessControl)
	d.AddString("session", rolloutDigests.SessionProps)
	d.AddString("secret", rolloutDigests.Secret)
	if includeCatalog {
		d.AddString("catalog", rolloutDigests.Catalog)
	}
	return d.Sum12()
}
