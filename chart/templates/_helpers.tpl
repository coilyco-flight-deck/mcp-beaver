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

{{/*
Whether a spec ConfigMap renders and mounts. Spec mode always mounts one.
Upstream mode mounts one when `spec` carries an `mcp-upstream` guardfile, which
is how a proxy stated as a file rather than as flags gets a deployment path.
Empty output is false to `if`.
*/}}
{{- define "mcp-beaver.mountsSpec" -}}
{{- if eq (default "spec" .Values.runtime.mode) "spec" -}}
true
{{- else if .Values.spec -}}
true
{{- end -}}
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

{{/*
A ConfigMap key for one widget, derived from the relative path the guardfile's
`app file=` names. Keys match [-._a-zA-Z0-9]+ and a path holds separators, so
the separators become underscores. Two paths deriving one key is a fail rather
than a silent overwrite - see configmap-spec.yaml.
*/}}
{{- define "mcp-beaver.widgetKey" -}}
{{- . | replace "/" "_" -}}
{{- end -}}

{{/*
Every file the spec ConfigMap projects into /spec, as a volume `items` list.
The volume enumerates rather than projecting the whole ConfigMap, because a
widget key is flattened and has to land back at the nested path the guardfile
names. Adding a key to the ConfigMap without adding it here leaves it unmounted.
*/}}
{{- define "mcp-beaver.specItems" -}}
- key: {{ printf "%s.mcp.kdl" (include "mcp-beaver.specName" .) }}
  path: {{ printf "%s.mcp.kdl" (include "mcp-beaver.specName" .) }}
{{- if .Values.apiDocument }}
- key: {{ .Values.apiDocumentName }}
  path: {{ .Values.apiDocumentName }}
{{- end }}
{{- range .Values.widgets }}
- key: {{ include "mcp-beaver.widgetKey" .path }}
  path: {{ .path }}
{{- end }}
{{- end -}}
