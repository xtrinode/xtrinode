{{/*
Expand the name of the chart.
*/}}
{{- define "xtrinode-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "xtrinode-operator.fullname" -}}
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
{{- define "xtrinode-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "xtrinode-operator.labels" -}}
helm.sh/chart: {{ include "xtrinode-operator.chart" . }}
{{ include "xtrinode-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "xtrinode-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xtrinode-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "xtrinode-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "xtrinode-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the admission webhook TLS secret.
*/}}
{{- define "xtrinode-operator.webhookSecretName" -}}
{{- default (printf "%s-webhook-tls" (include "xtrinode-operator.fullname" .)) .Values.webhook.certSecretName | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Return the image name respecting global.imageRegistry
*/}}
{{- define "xtrinode-operator.image" -}}
{{- $registry := .Values.global.imageRegistry | default "" -}}
{{- $repository := .Values.image.repository -}}
{{- $tag := (.Values.image.tag | default .Chart.AppVersion | toString) -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end }}
