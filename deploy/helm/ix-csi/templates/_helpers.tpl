{{/*
Expand the name of the chart.
*/}}
{{- define "ix-csi.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
Truncated at 63 chars (k8s name limit, DNS-1123).
*/}}
{{- define "ix-csi.fullname" -}}
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

{{- define "ix-csi.labels" -}}
app.kubernetes.io/name: {{ include "ix-csi.name" . }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "ix-csi.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ix-csi.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "ix-csi.driverName" -}}
csi.ix-csi.io
{{- end -}}

{{- define "ix-csi.controllerServiceAccountName" -}}
{{- if .Values.serviceAccount.controller.name -}}
{{- .Values.serviceAccount.controller.name -}}
{{- else -}}
{{- printf "%s-controller" (include "ix-csi.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "ix-csi.nodeServiceAccountName" -}}
{{- if .Values.serviceAccount.node.name -}}
{{- .Values.serviceAccount.node.name -}}
{{- else -}}
{{- printf "%s-node" (include "ix-csi.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "ix-csi.secretName" -}}
{{- if .Values.secret.existingSecret.name -}}
{{- .Values.secret.existingSecret.name -}}
{{- else if .Values.secret.name -}}
{{- .Values.secret.name -}}
{{- else -}}
{{- printf "%s-credentials" (include "ix-csi.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "ix-csi.secretKey" -}}
{{- if .Values.secret.existingSecret.name -}}
{{- default "TRUENAS_API_KEY" .Values.secret.existingSecret.key -}}
{{- else -}}
TRUENAS_API_KEY
{{- end -}}
{{- end -}}

{{- define "ix-csi.configMapName" -}}
{{- printf "%s-config" (include "ix-csi.fullname" .) -}}
{{- end -}}

{{/*
Resolve a driver image reference. A component-specific tag or digest takes
precedence over the chart-wide digest so controller and node PR images can be
tested independently of the release pin.
*/}}
{{- define "ix-csi.image" -}}
{{- $componentImage := default (dict) .component.image -}}
{{- if $componentImage.digest -}}
{{- printf "%s@%s" .root.Values.image.repository $componentImage.digest -}}
{{- else if $componentImage.tag -}}
{{- printf "%s:%s" .root.Values.image.repository $componentImage.tag -}}
{{- else if .root.Values.image.digest -}}
{{- printf "%s@%s" .root.Values.image.repository .root.Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .root.Values.image.repository (default .root.Chart.AppVersion .root.Values.image.tag) -}}
{{- end -}}
{{- end -}}

{{/*
Translate the chart's human-readable log level into Kubernetes klog verbosity.
*/}}
{{- define "ix-csi.logVerbosity" -}}
{{- $levels := dict "error" 0 "warning" 1 "info" 2 "debug" 4 -}}
{{- $level := required "logLevel must be one of: error, warning, info, debug" .Values.logLevel -}}
{{- if not (hasKey $levels $level) -}}
{{- fail (printf "logLevel must be one of: error, warning, info, debug; got %q" $level) -}}
{{- end -}}
{{- get $levels $level -}}
{{- end -}}
