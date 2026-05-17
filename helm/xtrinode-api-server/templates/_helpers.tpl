{{/*
Expand the name of the chart.
*/}}
{{- define "xtrinode-api-server.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create the name of the API bearer token Secret.
*/}}
{{- define "xtrinode-api-server.authSecretName" -}}
{{- coalesce .Values.apiServer.auth.existingSecret .Values.apiServer.auth.secretName (printf "%s-auth" (include "xtrinode-api-server.fullname" .)) -}}
{{- end }}

{{/*
Create the name of the resume-only API bearer token Secret.
*/}}
{{- define "xtrinode-api-server.resumeAuthSecretName" -}}
{{- coalesce .Values.apiServer.auth.resume.existingSecret .Values.apiServer.auth.resume.secretName (printf "%s-resume-auth" (include "xtrinode-api-server.fullname" .)) -}}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "xtrinode-api-server.fullname" -}}
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
{{- define "xtrinode-api-server.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "xtrinode-api-server.labels" -}}
helm.sh/chart: {{ include "xtrinode-api-server.chart" . }}
{{ include "xtrinode-api-server.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "xtrinode-api-server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xtrinode-api-server.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "xtrinode-api-server.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "xtrinode-api-server.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the image name respecting global.imageRegistry
*/}}
{{- define "xtrinode-api-server.image" -}}
{{- $registry := (index .Values "global" | default dict).imageRegistry | default "" -}}
{{- $repository := .Values.image.repository -}}
{{- $tag := (.Values.image.tag | default .Chart.AppVersion | toString) -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end }}
