########################################
# Provider and subscription targeting
########################################

variable "subscription_id" {
  description = "Azure subscription ID that owns the password-hook resources (see the private-network decision document)."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-fA-F-]{36}$", var.subscription_id))
    error_message = "subscription_id must be a 36-character GUID."
  }
}

variable "tenant_id" {
  description = "Azure Active Directory tenant ID for the subscription."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-fA-F-]{36}$", var.tenant_id))
    error_message = "tenant_id must be a 36-character GUID."
  }
}

########################################
# Environment, region, and naming
########################################

variable "environment" {
  description = "Deployment environment. Controls names and staging/production-specific policy."
  type        = string

  validation {
    condition     = contains(["stg", "prod"], var.environment)
    error_message = "environment must be one of: stg, prod."
  }
}

variable "location" {
  description = "Azure region for all resources created by this configuration."
  type        = string
  default     = "japaneast"

  validation {
    # Azure region short names are lowercase letters and digits with no spaces.
    condition     = can(regex("^[a-z0-9]+$", var.location))
    error_message = "location must be a lowercase Azure region short name (e.g. japaneast)."
  }
}

variable "name_prefix" {
  description = "Short lowercase prefix used to derive per-environment resource names before the state-stable random suffix."
  type        = string
  default     = "pwdhook"

  validation {
    # Keep short: Key Vault max 24 chars including suffix, so the raw prefix stays <=10.
    condition     = can(regex("^[a-z][a-z0-9]{2,9}$", var.name_prefix))
    error_message = "name_prefix must be 3-10 lowercase alphanumeric characters starting with a letter."
  }
}

########################################
# Resource group
########################################

variable "create_resource_group" {
  description = "When true, create the application resource group; when false, consume an existing one."
  type        = bool
  default     = false
}

variable "resource_group_name" {
  description = "Application resource group name. When create_resource_group is false, this must already exist."
  type        = string

  validation {
    # Azure resource group name rules: 1-90 chars; letters, digits, dot, dash, underscore, parentheses; may not end in dot.
    condition     = can(regex("^[A-Za-z0-9._()-]{1,90}$", var.resource_group_name)) && !endswith(var.resource_group_name, ".")
    error_message = "resource_group_name must be 1-90 chars from letters/digits/._()- and not end with a dot."
  }
}

########################################
# Runtime identity (created in root, passed to every module)
########################################

variable "runtime_identity_name" {
  description = "User-assigned managed identity name for the runtime workload."
  type        = string
  default     = "id-password-hook"

  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{2,127}$", var.runtime_identity_name))
    error_message = "runtime_identity_name must be 3-128 chars from letters/digits/_/- and start with a letter or digit."
  }
}

########################################
# Existing consumed resources (never created here)
########################################

variable "existing_acr_resource_id" {
  description = "Resource ID of the approved existing Azure Container Registry (e.g. .../rg-acr-jpe-001/.../registries/acrjpe001)."
  type        = string

  validation {
    condition     = can(regex("^/subscriptions/[0-9a-fA-F-]{36}/resourceGroups/[^/]+/providers/Microsoft\\.ContainerRegistry/registries/[a-zA-Z0-9]+$", var.existing_acr_resource_id))
    error_message = "existing_acr_resource_id must be a full ACR resource ID under Microsoft.ContainerRegistry/registries."
  }
}

variable "existing_container_app_environment_id" {
  description = "Resource ID of the approved existing Azure Container Apps managed environment."
  type        = string

  validation {
    condition     = can(regex("^/subscriptions/[0-9a-fA-F-]{36}/resourceGroups/[^/]+/providers/Microsoft\\.App/managedEnvironments/[^/]+$", var.existing_container_app_environment_id))
    error_message = "existing_container_app_environment_id must be a Microsoft.App/managedEnvironments resource ID."
  }
}

########################################
# Network mode and inputs approved by Task 0
########################################

variable "network_mode" {
  description = "Approved network topology. Only 'private_endpoints_in_existing_vnet' is supported: this repository creates a dedicated PE subnet plus private DNS zones inside an existing workload VNet it does not own; it never creates a VNet, VPN gateway, GatewaySubnet, DNS resolver, or Application Gateway."
  type        = string
  default     = "private_endpoints_in_existing_vnet"

  validation {
    condition     = var.network_mode == "private_endpoints_in_existing_vnet"
    error_message = "network_mode must be 'private_endpoints_in_existing_vnet' (the only mode approved in Task 0)."
  }
}

variable "existing_workload_vnet_id" {
  description = "Resource ID of the existing workload VNet that will host the new private-endpoint subnet and receive private DNS zone links."
  type        = string

  validation {
    condition     = can(regex("^/subscriptions/[0-9a-fA-F-]{36}/resourceGroups/[^/]+/providers/Microsoft\\.Network/virtualNetworks/[^/]+$", var.existing_workload_vnet_id))
    error_message = "existing_workload_vnet_id must be a Microsoft.Network/virtualNetworks resource ID."
  }
}

variable "private_endpoint_subnet_name" {
  description = "Name of the dedicated private-endpoint subnet this configuration creates inside the existing workload VNet."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$", var.private_endpoint_subnet_name))
    error_message = "private_endpoint_subnet_name must be 1-80 chars from letters/digits/._- and start with a letter or digit."
  }
}

variable "private_endpoint_subnet_cidr" {
  description = "CIDR (a subset of the existing workload VNet's address space) for the dedicated private-endpoint subnet."
  type        = string

  validation {
    condition     = can(cidrnetmask(var.private_endpoint_subnet_cidr))
    error_message = "private_endpoint_subnet_cidr must be a valid IPv4 CIDR (e.g. 10.0.4.224/27)."
  }
}

variable "private_dns_zone_resource_group_name" {
  description = "Resource group that owns the Private DNS zones this configuration creates (typically the central network/DNS resource group)."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9._()-]{1,90}$", var.private_dns_zone_resource_group_name)) && !endswith(var.private_dns_zone_resource_group_name, ".")
    error_message = "private_dns_zone_resource_group_name must be a valid resource-group name."
  }
}

variable "private_dns_zone_names" {
  description = "Names of the Private DNS zones for Key Vault, Service Bus, and Azure Managed Redis. Redis zone name must be verified against the pinned AzureRM provider before apply."
  type = object({
    key_vault     = string
    service_bus   = string
    managed_redis = string
  })
  default = {
    key_vault     = "privatelink.vaultcore.azure.net"
    service_bus   = "privatelink.servicebus.windows.net"
    managed_redis = "privatelink.redis.azure.net"
  }

  validation {
    condition = alltrue([
      can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.private_dns_zone_names.key_vault)),
      can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.private_dns_zone_names.service_bus)),
      can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.private_dns_zone_names.managed_redis)),
    ])
    error_message = "Each private DNS zone name must be a lowercase DNS-style name."
  }
}

########################################
# Application container image and rollout gate
########################################

variable "app_image" {
  description = "Fully qualified container image (registry/repository:tag) used by the Container App."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9./_-]+:[A-Za-z0-9._-]{1,128}$", var.app_image))
    error_message = "app_image must be of the form registry/repository:tag with a non-empty tag."
  }
}

variable "app_image_tag" {
  description = "Image tag applied to the Container App revision. Kept as a separate input so operators can pin/roll without changing the full image reference."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9._-]{1,128}$", var.app_image_tag))
    error_message = "app_image_tag must be 1-128 chars from letters/digits/._- (no 'latest' semantics enforced here)."
  }
}

variable "deploy_container_app" {
  description = "Explicit bootstrap gate. First apply runs with false to create identity/RBAC/network dependencies; a second apply with true creates the Container App revision after the image is pushed and RBAC has propagated."
  type        = bool
  default     = false
}

variable "container_app_min_replicas" {
  description = "Minimum replica count for the Container App."
  type        = number
  default     = 1

  validation {
    condition     = var.container_app_min_replicas >= 0 && var.container_app_min_replicas <= 25
    error_message = "container_app_min_replicas must be between 0 and 25."
  }
}

variable "container_app_max_replicas" {
  description = "Maximum replica count for the Container App."
  type        = number
  default     = 3

  validation {
    condition     = var.container_app_max_replicas >= 1 && var.container_app_max_replicas <= 25
    error_message = "container_app_max_replicas must be between 1 and 25."
  }
}

########################################
# Service Bus and password message TTL
########################################

variable "service_bus_queue_name" {
  description = "Active password-sync queue name."
  type        = string
  default     = "password-sync"

  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9._/-]{0,259}$", var.service_bus_queue_name))
    error_message = "service_bus_queue_name must be 1-260 chars starting with a letter or digit; letters/digits/._/- allowed."
  }
}

variable "service_bus_safe_dead_letter_queue_name" {
  description = "Application safe dead-letter queue name (distinct from the broker-native $DeadLetterQueue)."
  type        = string
  default     = "password-sync-safe-dlq"

  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9._/-]{0,259}$", var.service_bus_safe_dead_letter_queue_name))
    error_message = "service_bus_safe_dead_letter_queue_name must be 1-260 chars starting with a letter or digit; letters/digits/._/- allowed."
  }
}

variable "password_message_ttl_seconds" {
  description = "Single source of truth for the password message TTL. Rendered as the Service Bus ISO-8601 duration on the queue and as the Go duration passed to the app as PASSWORD_MESSAGE_TTL."
  type        = number
  default     = 300

  validation {
    condition     = var.password_message_ttl_seconds >= 60 && var.password_message_ttl_seconds <= 3600
    error_message = "password_message_ttl_seconds must be between 60 and 3600."
  }
}

########################################
# Redis sync-status retention
########################################

variable "sync_status_terminal_ttl_days" {
  description = "Owner-approved terminal-state retention for the Redis sync-status store (Task 0 selected 90 days)."
  type        = number
  default     = 90

  validation {
    condition     = var.sync_status_terminal_ttl_days >= 1 && var.sync_status_terminal_ttl_days <= 365
    error_message = "sync_status_terminal_ttl_days must be between 1 and 365."
  }
}

variable "redis_key_prefix" {
  description = "Redis key prefix for sync-status entries. Kept deployment-neutral by default."
  type        = string
  default     = "password-hook:sync-status:"

  validation {
    condition     = length(var.redis_key_prefix) > 0 && length(var.redis_key_prefix) <= 128
    error_message = "redis_key_prefix must be 1-128 chars."
  }
}

########################################
# Observability
########################################

variable "application_insights_retention_days" {
  description = "Application Insights data retention in days."
  type        = number
  default     = 90

  validation {
    condition     = contains([30, 60, 90, 120, 180, 270, 365, 550, 730], var.application_insights_retention_days)
    error_message = "application_insights_retention_days must be one of: 30, 60, 90, 120, 180, 270, 365, 550, 730."
  }
}

########################################
# Portal source-address and rate limits
########################################

variable "portal_allowed_cidrs" {
  description = "Approved portal caller CIDRs, enforced both at the Application Gateway WAF and by the application."
  type        = list(string)

  validation {
    condition     = length(var.portal_allowed_cidrs) > 0
    error_message = "portal_allowed_cidrs must contain at least one CIDR."
  }

  validation {
    condition     = alltrue([for c in var.portal_allowed_cidrs : can(cidrnetmask(c))])
    error_message = "Every portal_allowed_cidrs entry must be a valid IPv4 CIDR."
  }

  validation {
    # Guard against 0.0.0.0/0 or overly broad allowlists.
    condition     = alltrue([for c in var.portal_allowed_cidrs : tonumber(split("/", c)[1]) >= 24])
    error_message = "Every portal_allowed_cidrs entry must be /24 or smaller; broad allowlists are rejected."
  }
}

variable "trusted_proxy_cidrs" {
  description = "Immediate-peer CIDRs the application trusts for X-Forwarded-For parsing (typically the ACA/Application Gateway proxy peers). Must not overlap with portal_allowed_cidrs and must not be an unrestricted network."
  type        = list(string)

  validation {
    condition     = length(var.trusted_proxy_cidrs) > 0
    error_message = "trusted_proxy_cidrs must contain at least one CIDR."
  }

  validation {
    condition     = alltrue([for c in var.trusted_proxy_cidrs : can(cidrnetmask(c))])
    error_message = "Every trusted_proxy_cidrs entry must be a valid IPv4 CIDR."
  }

  validation {
    condition     = alltrue([for c in var.trusted_proxy_cidrs : tonumber(split("/", c)[1]) >= 16])
    error_message = "trusted_proxy_cidrs must not be broader than /16."
  }
}

variable "rate_limit_per_ip" {
  description = "Per-client rate limit (requests per window)."
  type        = number
  default     = 10

  validation {
    condition     = var.rate_limit_per_ip >= 1 && var.rate_limit_per_ip <= 10000
    error_message = "rate_limit_per_ip must be between 1 and 10000."
  }
}

variable "rate_limit_window" {
  description = "Rate-limit window as a Go duration (e.g. 1m, 30s)."
  type        = string
  default     = "1m"

  validation {
    condition     = can(regex("^[0-9]+(ns|us|ms|s|m|h)$", var.rate_limit_window))
    error_message = "rate_limit_window must be a Go duration (e.g. 30s, 1m, 1h)."
  }
}

########################################
# Graph identifiers and password encryption key
########################################

variable "entra_primary_domain" {
  description = "Primary Entra domain suffix (e.g. nycu.edu.tw)."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.entra_primary_domain))
    error_message = "entra_primary_domain must be a lowercase DNS-style domain."
  }
}

variable "entra_fallback_domain" {
  description = "Optional fallback Entra domain suffix. Empty string disables the fallback."
  type        = string
  default     = ""

  validation {
    condition     = var.entra_fallback_domain == "" || can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.entra_fallback_domain))
    error_message = "entra_fallback_domain must be empty or a lowercase DNS-style domain."
  }
}

variable "graph_tenant_id" {
  description = "Entra tenant ID used by the Graph client."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-fA-F-]{36}$", var.graph_tenant_id))
    error_message = "graph_tenant_id must be a 36-character GUID."
  }
}

variable "graph_client_id" {
  description = "Entra application (client) ID used by the Graph client."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-fA-F-]{36}$", var.graph_client_id))
    error_message = "graph_client_id must be a 36-character GUID."
  }
}

variable "password_encryption_key_id" {
  description = "Non-secret identifier for the operator-injected password payload encryption key."
  type        = string

  validation {
    condition     = length(var.password_encryption_key_id) >= 1 && length(var.password_encryption_key_id) <= 128
    error_message = "password_encryption_key_id must be 1-128 chars."
  }
}

########################################
# Application Gateway handoff contract (this repo does not manage the gateway)
########################################

variable "private_api_hostname" {
  description = "Private API hostname portal servers resolve to the Application Gateway private frontend (e.g. api.test.nycu.edu.tw)."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.private_api_hostname))
    error_message = "private_api_hostname must be a lowercase DNS-style hostname."
  }
}

variable "application_gateway_resource_id" {
  description = "Resource ID of the existing shared Application Gateway that will host the new private frontend/listener/rule (managed by the external owner pipeline, NOT this repository)."
  type        = string

  validation {
    condition     = can(regex("^/subscriptions/[0-9a-fA-F-]{36}/resourceGroups/[^/]+/providers/Microsoft\\.Network/applicationGateways/[^/]+$", var.application_gateway_resource_id))
    error_message = "application_gateway_resource_id must be a Microsoft.Network/applicationGateways resource ID."
  }
}

variable "application_gateway_listener_certificate_reference" {
  description = "Non-secret reference (name or Key Vault secret ID) identifying the TLS certificate the external Application Gateway owner binds to the private HTTPS listener."
  type        = string

  validation {
    condition     = length(var.application_gateway_listener_certificate_reference) > 0
    error_message = "application_gateway_listener_certificate_reference must not be empty."
  }
}

variable "application_gateway_private_frontend_ip" {
  description = "Requested static private frontend IPv4 address in the existing Application Gateway subnet. Reserved by the external Application Gateway owner. Must be an RFC 1918 private address; the slice is explicitly internal-only."
  type        = string

  validation {
    condition     = can(regex("^([0-9]{1,3}\\.){3}[0-9]{1,3}$", var.application_gateway_private_frontend_ip))
    error_message = "application_gateway_private_frontend_ip must be an IPv4 address."
  }

  validation {
    # Reject non-RFC 1918 addresses. This slice is internal-only, so the private
    # frontend IP must live in 10.0.0.0/8, 172.16.0.0/12, or 192.168.0.0/16.
    condition = (
      can(regex("^10\\.", var.application_gateway_private_frontend_ip)) ||
      can(regex("^172\\.(1[6-9]|2[0-9]|3[01])\\.", var.application_gateway_private_frontend_ip)) ||
      can(regex("^192\\.168\\.", var.application_gateway_private_frontend_ip))
    )
    error_message = "application_gateway_private_frontend_ip must be an RFC 1918 private address (10.0.0.0/8, 172.16.0.0/12, or 192.168.0.0/16)."
  }
}

variable "application_gateway_listener_priority" {
  description = "Requested HTTPS listener priority for the new private listener on the shared Application Gateway."
  type        = number

  validation {
    condition     = var.application_gateway_listener_priority >= 1 && var.application_gateway_listener_priority <= 20000
    error_message = "application_gateway_listener_priority must be between 1 and 20000."
  }
}

variable "application_gateway_rule_priority" {
  description = "Requested routing-rule priority (Task 0 recorded 120 for staging)."
  type        = number

  validation {
    condition     = var.application_gateway_rule_priority >= 1 && var.application_gateway_rule_priority <= 20000
    error_message = "application_gateway_rule_priority must be between 1 and 20000."
  }
}

variable "application_gateway_waf_policy" {
  description = "Requested WAF policy configuration for the dedicated password-hook listener on the shared Application Gateway. Emitted through the handoff-contract output so the external owner pipeline can reproduce it exactly. Defaults match the owner-approved decision doc (Prevention mode, OWASP 3.2 + BotManager 0.1, custom RemoteAddr Block rule at priority 10)."
  type = object({
    mode = string
    managed_rule_sets = list(object({
      type    = string
      version = string
    }))
    custom_block_rule = object({
      name     = string
      priority = number
      action   = string
    })
  })
  default = {
    mode = "Prevention"
    managed_rule_sets = [
      { type = "OWASP", version = "3.2" },
      { type = "Microsoft_BotManagerRuleSet", version = "0.1" },
    ]
    custom_block_rule = {
      name     = "BlockNonPortalSources"
      priority = 10
      action   = "Block"
    }
  }

  validation {
    condition     = contains(["Detection", "Prevention"], var.application_gateway_waf_policy.mode)
    error_message = "application_gateway_waf_policy.mode must be one of: Detection, Prevention."
  }

  validation {
    condition     = length(var.application_gateway_waf_policy.managed_rule_sets) > 0
    error_message = "application_gateway_waf_policy.managed_rule_sets must contain at least one entry."
  }

  validation {
    condition = alltrue([
      for r in var.application_gateway_waf_policy.managed_rule_sets :
      length(r.type) > 0 && length(r.version) > 0
    ])
    error_message = "Every application_gateway_waf_policy.managed_rule_sets entry must have non-empty type and version."
  }

  validation {
    condition = (
      var.application_gateway_waf_policy.custom_block_rule.priority >= 1 &&
      var.application_gateway_waf_policy.custom_block_rule.priority <= 100
    )
    error_message = "application_gateway_waf_policy.custom_block_rule.priority must be between 1 and 100."
  }

  validation {
    condition     = contains(["Block"], var.application_gateway_waf_policy.custom_block_rule.action)
    error_message = "application_gateway_waf_policy.custom_block_rule.action must be \"Block\". application-gateway-handoff.md forbids an \"Allow\" action that would bypass the OWASP/BotManager managed rule sets for approved sources; the rule's role is exclusively to block unapproved sources before the managed rules run."
  }

  validation {
    condition     = length(var.application_gateway_waf_policy.custom_block_rule.name) >= 1 && length(var.application_gateway_waf_policy.custom_block_rule.name) <= 128
    error_message = "application_gateway_waf_policy.custom_block_rule.name must be 1-128 chars."
  }
}

variable "application_gateway_backend_probe_path" {
  description = "HTTP path the Application Gateway probe uses against the ACA backend."
  type        = string
  default     = "/healthz"

  validation {
    condition     = can(regex("^/[A-Za-z0-9._~/-]*$", var.application_gateway_backend_probe_path))
    error_message = "application_gateway_backend_probe_path must start with '/'."
  }
}

########################################
# Key Vault operator access
########################################

variable "key_vault_operator_object_ids" {
  description = "Entra object IDs for named human operators who need Key Vault secret injection and rotation rights (Key Vault Secrets Officer). Leave empty on automated pipelines."
  type        = list(string)
  default     = []
}
