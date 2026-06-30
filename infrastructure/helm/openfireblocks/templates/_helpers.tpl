{{/* Common naming + label helpers. */}}

{{- define "ofb.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "ofb.labels" -}}
app.kubernetes.io/part-of: openfireblocks
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{/* Image reference for a component: registry/image:tag */}}
{{- define "ofb.image" -}}
{{- printf "%s/%s:%s" .root.Values.imageRegistry .image .tag -}}
{{- end -}}
