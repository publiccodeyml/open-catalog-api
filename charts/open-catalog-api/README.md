# open-catalog-api

![Version: 0.8.1](https://img.shields.io/badge/Version-0.8.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v1.4.0](https://img.shields.io/badge/AppVersion-v1.4.0-informational?style=flat-square)

RESTful API for public administration Open Source catalogs

## Requirements

Kubernetes: `>=1.23.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| autoscaling.enabled | bool | `false` |  |
| autoscaling.maxReplicas | int | `100` |  |
| autoscaling.minReplicas | int | `1` |  |
| autoscaling.targetCPUUtilizationPercentage | int | `80` |  |
| databaseDSN | string | `""` | Database connection string, e.g. "host= port=5432 dbname= user= password= sslmode=require". |
| deploymentAnnotations | object | `{}` |  |
| extraEnv | list | `[]` | Additional environment variables for the API container, as Kubernetes EnvVar objects, e.g. DATABASE_MAX_CONNECTIONS, TRUSTED_PROXIES or EVENT_RETENTION_DAYS. |
| extraVolumeMounts | list | `[]` |  |
| extraVolumes | list | `[]` |  |
| fullnameOverride | string | `""` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"ghcr.io/publiccodeyml/open-catalog-api"` |  |
| image.tag | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| ingress.annotations | object | `{}` |  |
| ingress.className | string | `""` |  |
| ingress.enabled | bool | `false` |  |
| ingress.hosts[0].host | string | `"chart-example.local"` |  |
| ingress.hosts[0].paths[0].path | string | `"/"` |  |
| ingress.hosts[0].paths[0].pathType | string | `"ImplementationSpecific"` |  |
| ingress.tls | list | `[]` |  |
| maxRequests | int | `nil` | Maximum number of requests per minute from each client IP. Behind a proxy, set TRUSTED_PROXIES in 'extraEnv' so the limit counts the original client address instead of the proxy's. |
| nameOverride | string | `""` |  |
| nodeSelector | object | `{}` |  |
| pasetoKey | string | `""` | Base64 encoded Paseto Key. |
| podAnnotations | object | `{}` |  |
| podSecurityContext.fsGroup | int | `10001` |  |
| podSecurityContext.runAsGroup | int | `10001` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.runAsUser | int | `10001` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| replicaCount | int | `1` |  |
| resources | object | `{}` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| service.port | int | `80` |  |
| service.type | string | `"ClusterIP"` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| serviceMonitor.additionalLabels | object | `{}` |  |
| serviceMonitor.enabled | bool | `false` | Create ServiceMonitor resource (requires corresponding Prometheus Operator CRD installed). |
| serviceMonitor.interval | string | `nil` | Interval at which metrics should be scraped (uses Prometheus default if unspecified, details [here](https://prometheus-operator.dev/docs/operator/api/#monitoring.coreos.com/v1.Endpoint)) |
| serviceMonitor.jobLabel | string | `nil` | Defaults to the name of the Kubernetes service (details [here](https://prometheus-operator.dev/docs/operator/api/#monitoring.coreos.com/v1.ServiceMonitor)). |
| serviceMonitor.scrapeTimeout | string | `nil` | Timeout after which the scrape is ended (uses Prometheus default if unspecified, details [here](https://prometheus-operator.dev/docs/operator/api/#monitoring.coreos.com/v1.Endpoint)). |
| serviceMonitor.targetLabels | list | `[]` |  |
| tolerations | list | `[]` |  |
| useExistingSecret | string | `nil` | Name of existing Kubernetes secret containing keys 'databaseDSN' and 'pasetoKey'. If not provided, a secret will be generated using values from 'databaseDSN' and 'pasetoKey'. Pods read the secret at startup, so restart them after changing an existing secret. |

