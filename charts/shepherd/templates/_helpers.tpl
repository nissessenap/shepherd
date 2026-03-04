{{/*
Expand the name of the chart.
*/}}
{{- define "shepherd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "shepherd.fullname" -}}
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
Allow the release namespace to be overridden
*/}}
{{- define "shepherd.namespace" -}}
{{ .Values.namespaceOverride | default .Release.Namespace }}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "shepherd.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "shepherd.labels" -}}
helm.sh/chart: {{ include "shepherd.chart" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: shepherd
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- with .Values.global.additionalLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Component labels - call with dict "context" . "component" "api"
*/}}
{{- define "shepherd.componentLabels" -}}
{{ include "shepherd.labels" .context }}
{{ include "shepherd.componentSelectorLabels" . }}
{{- end }}

{{/*
Component selector labels - call with dict "context" . "component" "api"
*/}}
{{- define "shepherd.componentSelectorLabels" -}}
app.kubernetes.io/name: {{ include "shepherd.name" .context }}
app.kubernetes.io/instance: {{ .context.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Base image reference (Loki pattern).
Usage: {{ include "shepherd.image" (dict "service" .Values.operator.image "global" .Values.global.image "defaultVersion" .Chart.AppVersion) }}
*/}}
{{- define "shepherd.image" -}}
{{- $registry := .global.registry | default .service.registry | default "" -}}
{{- $repository := .service.repository | default "" -}}
{{- $tag := .service.tag | default .defaultVersion | toString -}}
{{- if $registry -}}
  {{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
  {{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end -}}

{{/*
Create the name of the service account for a component
Usage: {{ include "shepherd.serviceAccountName" (dict "context" . "component" "api" "sa" .Values.api.serviceAccount) }}
*/}}
{{- define "shepherd.serviceAccountName" -}}
{{- if .sa.create }}
{{- default (printf "%s-%s" (include "shepherd.fullname" .context) .component) .sa.name }}
{{- else }}
{{- default "default" .sa.name }}
{{- end }}
{{- end }}
