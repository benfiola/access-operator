{{- define "operator.name" -}}
{{- default .Release.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "operator.labels" -}}
helm.sh/chart: {{  printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | replace "+" "-" | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "operator.image" -}}
{{ $tag := .Chart.AppVersion | replace "+" "-" }}
{{ $repo := "docker.io/benfiola/access-operator" }}
{{ printf "%s:%s" $repo $tag }}
{{- end }}

{{- define "operator.operator.name" -}}
{{ printf "%s-operator" (include "operator.name" .)}}
{{- end }}

{{- define "operator.operator.labels" -}}
{{ include "operator.labels" . }}
{{ include "operator.operator.selectorLabels" . }}
{{- end }}

{{- define "operator.operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
bfiola.dev/component: operator
{{- end }}

{{- define "operator.server.name" -}}
{{ printf "%s-server" (include "operator.name" .)}}
{{- end }}

{{- define "operator.server.labels" -}}
{{ include "operator.labels" . }}
{{ include "operator.server.selectorLabels" . }}
{{- end }}

{{- define "operator.server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
bfiola.dev/component: server
{{- end }}
