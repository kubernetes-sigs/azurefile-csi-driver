{{/* vim: set filetype=mustache: */}}

{{/* Expand the name of the chart.*/}}
{{- define "azurefile.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "azurefile.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common selectors.
*/}}
{{- define "azurefile.selectorLabels" -}}
app.kubernetes.io/name: {{ template "azurefile.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "azurefile.labels" -}}
{{- include "azurefile.selectorLabels" . }}
app.kubernetes.io/component: csi-driver
app.kubernetes.io/part-of: {{ template "azurefile.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
helm.sh/chart: {{ template "azurefile.chart" . }}
{{- if .Values.customLabels }}
{{ toYaml .Values.customLabels }}
{{- end }}
{{- end -}}


{{/* pull secrets for containers */}}
{{- define "azurefile.pullSecrets" -}}
{{- if .Values.imagePullSecrets }}
imagePullSecrets:
{{- range .Values.imagePullSecrets }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end -}}
