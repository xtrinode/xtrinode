{{/*
Expand the name of the chart.
*/}}
{{- define "xtrinode-gateway.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "xtrinode-gateway.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "xtrinode-gateway.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "xtrinode-gateway.labels" -}}
helm.sh/chart: {{ include "xtrinode-gateway.chart" . }}
{{ include "xtrinode-gateway.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "xtrinode-gateway.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xtrinode-gateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Gateway pod selector labels.
*/}}
{{- define "xtrinode-gateway.gatewaySelectorLabels" -}}
{{ include "xtrinode-gateway.selectorLabels" . }}
app.kubernetes.io/component: gateway
{{- end }}

{{/*
Namespace used by this chart.
*/}}
{{- define "xtrinode-gateway.namespace" -}}
{{- default .Release.Namespace .Values.namespaceOverride -}}
{{- end }}

{{/*
Create the name of the API server bearer token Secret.
*/}}
{{- define "xtrinode-gateway.apiServerAuthSecretName" -}}
{{- coalesce .Values.gateway.apiServerAuth.existingSecret .Values.gateway.apiServerAuth.secretName (printf "%s-api-server-auth" (include "xtrinode-gateway.fullname" .)) -}}
{{- end }}

{{/*
Redis pod selector labels.
*/}}
{{- define "xtrinode-gateway.redisSelectorLabels" -}}
{{ include "xtrinode-gateway.selectorLabels" . }}
app.kubernetes.io/component: redis
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "xtrinode-gateway.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "xtrinode-gateway.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the image name respecting global.imageRegistry
*/}}
{{- define "xtrinode-gateway.image" -}}
{{- $registry := (index .Values "global" | default dict).imageRegistry | default "" -}}
{{- $repository := .Values.image.repository -}}
{{- $tag := (.Values.image.tag | default .Chart.AppVersion | toString) -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end }}

{{/*
Redis service name for optional in-chart Redis.
*/}}
{{- define "xtrinode-gateway.redisName" -}}
{{- printf "%s-redis" (include "xtrinode-gateway.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Redis Secret name.
*/}}
{{- define "xtrinode-gateway.redisSecretName" -}}
{{- if .Values.redis.auth.existingSecret -}}
{{- .Values.redis.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-auth" (include "xtrinode-gateway.redisName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{/*
Gateway Redis URL. Uses an explicitly supplied URL first, then the in-chart Redis service.
*/}}
{{- define "xtrinode-gateway.redisURL" -}}
{{- if .Values.gateway.redis.url -}}
{{- .Values.gateway.redis.url -}}
{{- else -}}
{{- printf "redis://%s.%s.svc.cluster.local:%v/%v" (include "xtrinode-gateway.redisName" .) (include "xtrinode-gateway.namespace" .) (.Values.redis.service.port | default 6379) (.Values.gateway.redis.db | default 0) -}}
{{- end -}}
{{- end }}
