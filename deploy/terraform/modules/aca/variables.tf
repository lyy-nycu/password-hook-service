########################################
# Placeholder inputs for the ACA / Container App module. Task 6 replaces
# this stub with the real Log Analytics + Application Insights + managed
# OpenTelemetry agent + Container App + Azure Monitor metrics wiring.
########################################

variable "container_app_name" {
  description = "Container App name."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
}

variable "resource_group_name" {
  description = "Application resource group."
  type        = string
}

variable "container_app_environment_id" {
  description = "Existing ACA managed environment resource ID."
  type        = string
}

variable "existing_acr_login_server" {
  description = "Existing ACR login server."
  type        = string
}

variable "existing_acr_resource_id" {
  description = "Existing ACR resource ID for AcrPull scope."
  type        = string
}

variable "runtime_identity_id" {
  description = "Runtime UAMI resource ID (attached to the Container App)."
  type        = string
}

variable "runtime_identity_client_id" {
  description = "Runtime UAMI client ID (AZURE_CLIENT_ID env var)."
  type        = string
}

variable "runtime_identity_principal_id" {
  description = "Runtime UAMI principal ID for RBAC assignments."
  type        = string
}

variable "deploy_container_app" {
  description = "Bootstrap gate: false skips Container App creation."
  type        = bool
}

variable "image" {
  description = "Container image (registry/repo:tag)."
  type        = string
}

variable "image_tag" {
  description = "Container image tag."
  type        = string
}

variable "min_replicas" {
  description = "Minimum replicas."
  type        = number
}

variable "max_replicas" {
  description = "Maximum replicas."
  type        = number
}

variable "service_bus_namespace_fqdn" {
  description = "Service Bus namespace FQDN (SERVICEBUS_NAMESPACE_FQDN)."
  type        = string
}

variable "service_bus_queue_name" {
  description = "Active Service Bus queue name."
  type        = string
}

variable "safe_dead_letter_queue_name" {
  description = "Safe dead-letter Service Bus queue name (SERVICEBUS_DEADLETTER_QUEUE_NAME)."
  type        = string
}

variable "password_message_ttl" {
  description = "PASSWORD_MESSAGE_TTL as a Go duration."
  type        = string
}

variable "redis_host" {
  description = "REDIS_HOST value."
  type        = string
}

variable "redis_port" {
  description = "REDIS_PORT value."
  type        = number
}

variable "redis_key_prefix" {
  description = "REDIS_KEY_PREFIX value."
  type        = string
}

variable "sync_status_terminal_ttl" {
  description = "SYNC_STATUS_TERMINAL_TTL as a Go duration."
  type        = string
}

variable "portal_allowed_cidrs" {
  description = "PORTAL_ALLOWED_CIDRS list."
  type        = list(string)
}

variable "trusted_proxy_cidrs" {
  description = "TRUSTED_PROXY_CIDRS list."
  type        = list(string)
}

variable "rate_limit_per_ip" {
  description = "RATE_LIMIT_PER_IP value."
  type        = number
}

variable "rate_limit_window" {
  description = "RATE_LIMIT_WINDOW value (Go duration)."
  type        = string
}

variable "entra_primary_domain" {
  description = "ENTRA_PRIMARY_DOMAIN value."
  type        = string
}

variable "entra_fallback_domain" {
  description = "ENTRA_FALLBACK_DOMAIN value."
  type        = string
}

variable "graph_tenant_id" {
  description = "GRAPH_TENANT_ID value."
  type        = string
}

variable "graph_client_id" {
  description = "GRAPH_CLIENT_ID value."
  type        = string
}

variable "password_encryption_key_id" {
  description = "PASSWORD_ENCRYPTION_KEY_ID value."
  type        = string
}

variable "application_insights_retention_days" {
  description = "App Insights data retention in days."
  type        = number
}

variable "log_analytics_name" {
  description = "Log Analytics workspace name."
  type        = string
}

variable "application_insights_name" {
  description = "Application Insights component name."
  type        = string
}

variable "key_vault_uri" {
  description = "Key Vault URI (KEY_VAULT_URL)."
  type        = string
}

variable "key_vault_secret_names" {
  description = "Non-secret Key Vault secret names for the runtime env vars (KEY_VAULT_HMAC_SECRET_NAME / KEY_VAULT_GRAPH_CLIENT_SECRET_NAME / KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME). Values must match module.keyvault.expected_secret_names."
  type = object({
    hmac_secret                     = string
    graph_client_secret             = string
    password_payload_encryption_key = string
  })
}

variable "tags" {
  description = "Common tags."
  type        = map(string)
  default     = {}
}
