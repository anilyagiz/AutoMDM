{{/*
Expand the name of the chart.
*/}}
{{- define "automdm.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "automdm.fullname" -}}
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

{{- define "automdm.servicename" -}}
{{- $fullName := include "automdm.fullname" . -}}
{{- printf "%s-service" $fullName }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "automdm.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "automdm.labels" -}}
helm.sh/chart: {{ include "automdm.chart" . }}
{{ include "automdm.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "automdm.selectorLabels" -}}
app.kubernetes.io/name: {{ include "automdm.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "automdm.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "automdm.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "automdm.additionalCAs.enabled" -}}
{{- if hasKey .Values.automdm "additionalCAs" -}}
{{- if and .Values.automdm.additionalCAs.enabled (or (and (.Values.automdm.additionalCAs.configMaps) (gt (len .Values.automdm.additionalCAs.configMaps) 0)) (and (.Values.automdm.additionalCAs.secrets) (gt (len .Values.automdm.additionalCAs.secrets) 0))) -}}
true
{{- end -}}
{{- end -}}
{{- end -}}
