{{/*
Expand the name of the chart.
*/}}
{{- define "operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "operator.fullname" -}}
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
{{- define "operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "operator.labels" -}}
helm.sh/chart: {{ include "operator.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | replace "+" "-" | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Calculate the image tag to use
*/}}
{{- define "operator.imageTag" -}}
{{ .Values.image.tag | default (.Chart.AppVersion | replace "+" "-") }}
{{- end }}

{{/*
Create an operator fullname
*/}}
{{- define "operator.operator.fullname" -}}
{{ printf "%s-operator" (include "operator.fullname" .)}}
{{- end }}

{{/*
Common operator labels
*/}}
{{- define "operator.operator.labels" -}}
{{ include "operator.labels" . }}
{{ include "operator.operator.selectorLabels" . }}
{{- end }}

{{/*
Operator Selector labels
*/}}
{{- define "operator.operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
bfiola.dev/component: operator
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "operator.operator.serviceAccountName" -}}
{{- if .Values.operator.serviceAccount.create }}
{{- default (printf "%s-operator" (include "operator.fullname" .)) .Values.operator.serviceAccount.name }}
{{- else }}
{{- default "default-operator" .Values.operator.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the operator ClusterRole to use
*/}}
{{- define "operator.operator.clusterRoleName" -}}
{{- default (printf "%s-operator" (include "operator.fullname" .)) .Values.operator.rbac.name }}
{{- end }}

{{/*
Create an server fullname
*/}}
{{- define "operator.server.fullname" -}}
{{ printf "%s-server" (include "operator.fullname" .)}}
{{- end }}

{{/*
Common server labels
*/}}
{{- define "operator.server.labels" -}}
{{ include "operator.labels" . }}
{{ include "operator.server.selectorLabels" . }}
{{- end }}

{{/*
Server Selector labels
*/}}
{{- define "operator.server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
bfiola.dev/component: server
{{- end }}

{{/*
Create the name of the server service account to use
*/}}
{{- define "operator.server.serviceAccountName" -}}
{{- if .Values.server.serviceAccount.create }}
{{- default (printf "%s-server" (include "operator.fullname" .)) .Values.server.serviceAccount.name }}
{{- else }}
{{- default "default-server" .Values.server.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the server ClusterRole to use
*/}}
{{- define "operator.server.clusterRoleName" -}}
{{- default (printf "%s-server" (include "operator.fullname" .)) .Values.server.rbac.name }}
{{- end }}
