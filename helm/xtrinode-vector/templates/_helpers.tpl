{{/*
Expand the name of the chart.
*/}}
{{- define "xtrinode-vector.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "xtrinode-vector.fullname" -}}
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
{{- define "xtrinode-vector.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Target namespace for Vector resources.
*/}}
{{- define "xtrinode-vector.namespace" -}}
{{- default .Release.Namespace .Values.namespaceOverride }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "xtrinode-vector.labels" -}}
helm.sh/chart: {{ include "xtrinode-vector.chart" . }}
{{ include "xtrinode-vector.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: xtrinode
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "xtrinode-vector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xtrinode-vector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: log-aggregator
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "xtrinode-vector.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "xtrinode-vector.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the image name.
*/}}
{{- define "xtrinode-vector.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion | toString) -}}
{{- end }}
