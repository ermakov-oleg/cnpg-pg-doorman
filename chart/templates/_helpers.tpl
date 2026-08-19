{{/*
Resource name. The plugin is a per-cluster singleton (fixed plugin name
pg-doorman.cnpg.io, ClusterRole), so a fixed default keeps the rendered
output identical to the kustomize manifests.
*/}}
{{- define "cnpg-pg-doorman.fullname" -}}
{{- default "pg-doorman" .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Selector labels: kept minimal because selectors are immutable. */}}
{{- define "cnpg-pg-doorman.selectorLabels" -}}
app: {{ include "cnpg-pg-doorman.fullname" . }}
{{- end -}}

{{/* Common labels. */}}
{{- define "cnpg-pg-doorman.labels" -}}
{{ include "cnpg-pg-doorman.selectorLabels" . }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Plugin image reference; tag defaults to the chart appVersion. */}}
{{- define "cnpg-pg-doorman.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}

{{/* Sidecar wrapper image reference; tag defaults to the chart appVersion. */}}
{{- define "cnpg-pg-doorman.sidecarImage" -}}
{{ .Values.sidecarImage.repository }}:{{ .Values.sidecarImage.tag | default .Chart.AppVersion }}
{{- end -}}
