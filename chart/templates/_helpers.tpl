{{- define "mcp-beaver.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
A release named "mcp-beaver" against chart "mcp-beaver" would double up, so a
release name that already contains the chart name is used as-is.
*/}}
{{- define "mcp-beaver.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "mcp-beaver.labels" -}}
app.kubernetes.io/name: {{ include "mcp-beaver.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "mcp-beaver.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mcp-beaver.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "mcp-beaver.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{/* The <name> in /spec/<name>.mcp.kdl - specName override, else release name. */}}
{{- define "mcp-beaver.specName" -}}
{{- default .Release.Name .Values.specName -}}
{{- end -}}

{{/* The in-container path the runtime reads the guardfile from. */}}
{{- define "mcp-beaver.specPath" -}}
{{- printf "/spec/%s.mcp.kdl" (include "mcp-beaver.specName" .) -}}
{{- end -}}

{{/* Secret name the string-form `secret` entries are materialized into. */}}
{{- define "mcp-beaver.secretName" -}}
{{- printf "%s-secret" (include "mcp-beaver.fullname" .) -}}
{{- end -}}

{{/*
Do any `secret` entries need a chart-minted ExternalSecret? True when at least
one value is a bare string (an SSM parameter path) rather than a
{secretName,key} reference to an already-existing Secret.
*/}}
{{- define "mcp-beaver.hasSsmSecret" -}}
{{- $ssm := false -}}
{{- range $env, $src := .Values.secret -}}
{{- if kindIs "string" $src -}}{{- $ssm = true -}}{{- end -}}
{{- end -}}
{{- $ssm -}}
{{- end -}}
