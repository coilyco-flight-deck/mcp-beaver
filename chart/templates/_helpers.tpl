{{- define "ward-mcp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
A release named "ward-mcp" against chart "ward-mcp" would double up, so a
release name that already contains the chart name is used as-is.
*/}}
{{- define "ward-mcp.fullname" -}}
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

{{- define "ward-mcp.labels" -}}
app.kubernetes.io/name: {{ include "ward-mcp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "ward-mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ward-mcp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "ward-mcp.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{/* The <name> in /spec/<name>.mcp.kdl - specName override, else release name. */}}
{{- define "ward-mcp.specName" -}}
{{- default .Release.Name .Values.specName -}}
{{- end -}}

{{/* The in-container path the runtime reads the guardfile from. */}}
{{- define "ward-mcp.specPath" -}}
{{- printf "/spec/%s.mcp.kdl" (include "ward-mcp.specName" .) -}}
{{- end -}}

{{/* Secret name the string-form `secret` entries are materialized into. */}}
{{- define "ward-mcp.secretName" -}}
{{- printf "%s-secret" (include "ward-mcp.fullname" .) -}}
{{- end -}}

{{/*
Do any `secret` entries need a chart-minted ExternalSecret? True when at least
one value is a bare string (an SSM parameter path) rather than a
{secretName,key} reference to an already-existing Secret.
*/}}
{{- define "ward-mcp.hasSsmSecret" -}}
{{- $ssm := false -}}
{{- range $env, $src := .Values.secret -}}
{{- if kindIs "string" $src -}}{{- $ssm = true -}}{{- end -}}
{{- end -}}
{{- $ssm -}}
{{- end -}}
