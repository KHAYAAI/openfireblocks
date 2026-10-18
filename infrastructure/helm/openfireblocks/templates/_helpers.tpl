{{/* Common naming + label helpers. */}}

{{- define "ofb.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "ofb.labels" -}}
app.kubernetes.io/part-of: openfireblocks
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{/* Image reference for a component: registry/image:tag.
     A chart-wide imageTag, when set, overrides every component's own tag, so a
     release deploys one commit's images with a single --set instead of one per
     component. */}}
{{- define "ofb.image" -}}
{{- $tag := .tag -}}
{{- if .root.Values.imageTag -}}{{- $tag = .root.Values.imageTag -}}{{- end -}}
{{- printf "%s/%s:%s" .root.Values.imageRegistry .image $tag -}}
{{- end -}}
