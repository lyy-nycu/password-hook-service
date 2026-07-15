terraform {
  required_version = ">= 1.8.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
    azapi = {
      source  = "Azure/azapi"
      version = "~> 2.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

provider "azurerm" {
  subscription_id = var.subscription_id
  tenant_id       = var.tenant_id

  features {
    key_vault {
      purge_soft_delete_on_destroy    = false
      recover_soft_deleted_key_vaults = true
    }
    resource_group {
      prevent_deletion_if_contains_resources = true
    }
  }
}

provider "azapi" {
  subscription_id = var.subscription_id
  tenant_id       = var.tenant_id
}

########################################
# State-stable random suffix for globally scoped resource names.
#
# NOTE: This suffix is generated once and PERSISTED IN TERRAFORM STATE.
# It is random (not deterministic from any input) and is intentionally
# stable across applies. Losing state produces a different suffix and
# would re-create every globally scoped resource that consumes it, so
# treat the state as production critical.
########################################

resource "random_string" "name_suffix" {
  length  = 6
  lower   = true
  upper   = false
  numeric = true
  special = false
}

########################################
# Application resource group: create OR consume an existing one.
########################################

resource "azurerm_resource_group" "this" {
  count    = var.create_resource_group ? 1 : 0
  name     = var.resource_group_name
  location = var.location
}

data "azurerm_resource_group" "existing" {
  count = var.create_resource_group ? 0 : 1
  name  = var.resource_group_name
}

########################################
# Existing consumed resources: read-only references only.
########################################

data "azurerm_container_registry" "existing" {
  name                = local.existing_acr_name
  resource_group_name = local.existing_acr_resource_group
}

data "azurerm_container_app_environment" "existing" {
  name                = local.existing_container_app_environment_name
  resource_group_name = local.existing_container_app_environment_resource_group
}

########################################
# Runtime identity (created once in the root, passed to every module).
########################################

resource "azurerm_user_assigned_identity" "runtime" {
  name                = var.runtime_identity_name
  location            = local.resource_group_location
  resource_group_name = local.resource_group_name
}

########################################
# Derived local values: names, parsed resource IDs, ISO-8601 TTL, tags.
########################################

locals {
  resource_group_name     = var.create_resource_group ? azurerm_resource_group.this[0].name : data.azurerm_resource_group.existing[0].name
  resource_group_location = var.create_resource_group ? azurerm_resource_group.this[0].location : data.azurerm_resource_group.existing[0].location

  # Parse the existing-resource IDs into (resource_group, name) tuples so we
  # never re-encode ownership; data sources look them up read-only.
  acr_id_parts                = split("/", var.existing_acr_resource_id)
  existing_acr_resource_group = local.acr_id_parts[4]
  existing_acr_name           = element(local.acr_id_parts, length(local.acr_id_parts) - 1)

  cae_id_parts                                      = split("/", var.existing_container_app_environment_id)
  existing_container_app_environment_resource_group = local.cae_id_parts[4]
  existing_container_app_environment_name           = element(local.cae_id_parts, length(local.cae_id_parts) - 1)

  vnet_id_parts                         = split("/", var.existing_workload_vnet_id)
  existing_workload_vnet_resource_group = local.vnet_id_parts[4]
  existing_workload_vnet_name           = element(local.vnet_id_parts, length(local.vnet_id_parts) - 1)

  # Normalized names for created resources. Every name uses <prefix>-<env>-<suffix>
  # or a globally scoped compact form so it stays inside Azure's per-service limits.
  name_suffix = random_string.name_suffix.result

  # Compact form for globally scoped names with tight length budgets (Key Vault, Service Bus, Managed Redis, Log Analytics/App Insights).
  compact_base = "${var.name_prefix}${var.environment}${local.name_suffix}"

  # Distinct per-resource normalized names.
  key_vault_name                 = substr("kv${local.compact_base}", 0, 24)                                        # Key Vault: 3-24 chars, letters/digits/dash
  service_bus_namespace_name     = substr("sb-${var.name_prefix}-${var.environment}-${local.name_suffix}", 0, 50)  # 6-50 chars
  managed_redis_name             = substr("redis${local.compact_base}", 0, 63)                                     # <= 63 chars
  log_analytics_name             = substr("log-${var.name_prefix}-${var.environment}-${local.name_suffix}", 0, 63) # <= 63 chars
  application_insights_name      = substr("appi-${var.name_prefix}-${var.environment}-${local.name_suffix}", 0, 260)
  container_app_environment_name = substr("cae-${var.name_prefix}-${var.environment}-${local.name_suffix}", 0, 32) # ACA env: <= 32
  container_app_name             = substr("ca-${var.name_prefix}-${var.environment}-${local.name_suffix}", 0, 32)  # Container App: <= 32

  # Password message TTL rendered in both forms so app config and Service Bus stay in sync.
  password_message_ttl_iso8601     = "PT${var.password_message_ttl_seconds}S"
  password_message_ttl_go_duration = "${var.password_message_ttl_seconds}s"

  # Redis terminal TTL Go duration derived from the single day-scoped input.
  sync_status_terminal_ttl_go_duration = "${var.sync_status_terminal_ttl_days * 24}h"

  # Length guards are enforced by name_length_guards below via precondition
  # blocks; keeping the checks in a resource makes them fail at plan time
  # before any provider write.

  common_tags = {
    "application" = "password-hook-service"
    "environment" = var.environment
    "managed-by"  = "terraform"
  }
}

resource "terraform_data" "name_length_guards" {
  # Trigger re-evaluation whenever any derived name changes so the
  # preconditions fire during plan on every relevant change.
  input = {
    key_vault_name                 = local.key_vault_name
    service_bus_namespace_name     = local.service_bus_namespace_name
    managed_redis_name             = local.managed_redis_name
    container_app_environment_name = local.container_app_environment_name
    container_app_name             = local.container_app_name
    log_analytics_name             = local.log_analytics_name
    application_insights_name      = local.application_insights_name
    app_image                      = var.app_image
    app_image_tag                  = var.app_image_tag
    existing_acr_login_server      = data.azurerm_container_registry.existing.login_server
  }

  lifecycle {
    precondition {
      condition     = length(local.key_vault_name) >= 3 && length(local.key_vault_name) <= 24
      error_message = "Derived key_vault_name must be 3-24 chars (Azure Key Vault limit); shorten name_prefix."
    }
    precondition {
      condition     = length(local.service_bus_namespace_name) >= 6 && length(local.service_bus_namespace_name) <= 50
      error_message = "Derived service_bus_namespace_name must be 6-50 chars (Azure Service Bus limit)."
    }
    precondition {
      condition     = length(local.managed_redis_name) >= 1 && length(local.managed_redis_name) <= 63
      error_message = "Derived managed_redis_name must be 1-63 chars (Azure Managed Redis limit)."
    }
    precondition {
      condition     = length(local.container_app_environment_name) >= 2 && length(local.container_app_environment_name) <= 32
      error_message = "Derived container_app_environment_name must be 2-32 chars (Azure Container Apps environment limit)."
    }
    precondition {
      condition     = length(local.container_app_name) >= 2 && length(local.container_app_name) <= 32
      error_message = "Derived container_app_name must be 2-32 chars (Azure Container Apps limit)."
    }
    precondition {
      condition     = length(local.log_analytics_name) >= 4 && length(local.log_analytics_name) <= 63
      error_message = "Derived log_analytics_name must be 4-63 chars (Log Analytics workspace limit)."
    }
    precondition {
      condition     = length(local.application_insights_name) >= 1 && length(local.application_insights_name) <= 260
      error_message = "Derived application_insights_name must be 1-260 chars."
    }
    precondition {
      condition     = length(setintersection(toset(var.portal_allowed_cidrs), toset(var.trusted_proxy_cidrs))) == 0
      error_message = "portal_allowed_cidrs and trusted_proxy_cidrs must not share any CIDR (matches internal/config/config.go enforcement)."
    }
    precondition {
      condition     = var.container_app_min_replicas <= var.container_app_max_replicas
      error_message = "container_app_min_replicas must be <= container_app_max_replicas."
    }
    precondition {
      # Bind app_image to the approved existing ACR so an operator cannot deploy an
      # image from a different registry than the one whose AcrPull role assignment
      # this configuration creates.
      condition     = split("/", var.app_image)[0] == data.azurerm_container_registry.existing.login_server
      error_message = "app_image registry (${split("/", var.app_image)[0]}) must equal the approved existing ACR login server (${data.azurerm_container_registry.existing.login_server})."
    }
    precondition {
      # Keep the embedded tag in app_image identical to app_image_tag so operators
      # cannot pin/roll one without the other.
      condition     = element(split(":", var.app_image), length(split(":", var.app_image)) - 1) == var.app_image_tag
      error_message = "app_image tag (${element(split(":", var.app_image), length(split(":", var.app_image)) - 1)}) must equal app_image_tag (${var.app_image_tag})."
    }
  }
}

########################################
# Module wiring
#
# Task 2 wires the network, servicebus, keyvault, redis, and aca modules.
# All non-network modules are still empty stubs; their actual resource
# logic is implemented by Tasks 3, 4, 5, and 6. This root only supplies
# their inputs and a stable call graph. `terraform validate` at the
# root is deferred until every module task is complete.
#
# There is no Application Gateway module: the shared gateway is managed
# by the external owner pipeline (lyy-nycu/ldap-service). This root only
# emits the handoff-contract outputs.
########################################

module "network" {
  source = "./modules/network"

  environment                          = var.environment
  location                             = local.resource_group_location
  private_endpoint_subnet_name         = var.private_endpoint_subnet_name
  private_endpoint_subnet_cidr         = var.private_endpoint_subnet_cidr
  workload_vnet_id                     = var.existing_workload_vnet_id
  workload_vnet_name                   = local.existing_workload_vnet_name
  workload_vnet_resource_group_name    = local.existing_workload_vnet_resource_group
  private_dns_zone_resource_group_name = var.private_dns_zone_resource_group_name
  private_dns_zone_names               = var.private_dns_zone_names
  tags                                 = local.common_tags
}

module "servicebus" {
  source = "./modules/servicebus"

  namespace_name                = local.service_bus_namespace_name
  location                      = local.resource_group_location
  resource_group_name           = local.resource_group_name
  queue_name                    = var.service_bus_queue_name
  safe_dead_letter_queue_name   = var.service_bus_safe_dead_letter_queue_name
  message_ttl_iso8601           = local.password_message_ttl_iso8601
  runtime_identity_principal_id = azurerm_user_assigned_identity.runtime.principal_id
  private_endpoint_subnet_id    = module.network.private_endpoint_subnet_id
  private_dns_zone_ids          = module.network.private_dns_zone_ids
  tags                          = local.common_tags
}

module "keyvault" {
  source = "./modules/keyvault"

  vault_name                    = local.key_vault_name
  location                      = local.resource_group_location
  resource_group_name           = local.resource_group_name
  tenant_id                     = var.tenant_id
  runtime_identity_principal_id = azurerm_user_assigned_identity.runtime.principal_id
  private_endpoint_subnet_id    = module.network.private_endpoint_subnet_id
  private_dns_zone_ids          = module.network.private_dns_zone_ids
  operator_object_ids           = var.key_vault_operator_object_ids
  tags                          = local.common_tags
}

module "redis" {
  source = "./modules/redis"

  managed_redis_name            = local.managed_redis_name
  location                      = local.resource_group_location
  resource_group_name           = local.resource_group_name
  runtime_identity_principal_id = azurerm_user_assigned_identity.runtime.principal_id
  private_endpoint_subnet_id    = module.network.private_endpoint_subnet_id
  private_dns_zone_ids          = module.network.private_dns_zone_ids
  tags                          = local.common_tags
}

module "aca" {
  source = "./modules/aca"

  # Placement in existing shared ACA environment (never created here).
  container_app_name           = local.container_app_name
  location                     = local.resource_group_location
  resource_group_name          = local.resource_group_name
  container_app_environment_id = var.existing_container_app_environment_id
  existing_acr_login_server    = data.azurerm_container_registry.existing.login_server
  existing_acr_resource_id     = var.existing_acr_resource_id

  # Runtime identity.
  runtime_identity_id           = azurerm_user_assigned_identity.runtime.id
  runtime_identity_client_id    = azurerm_user_assigned_identity.runtime.client_id
  runtime_identity_principal_id = azurerm_user_assigned_identity.runtime.principal_id

  # Image / rollout gate.
  deploy_container_app = var.deploy_container_app
  image                = var.app_image
  image_tag            = var.app_image_tag
  min_replicas         = var.container_app_min_replicas
  max_replicas         = var.container_app_max_replicas

  # Non-secret runtime configuration (Redis, Service Bus, portal enforcement).
  service_bus_namespace_fqdn = module.servicebus.namespace_fqdn
  service_bus_queue_name     = module.servicebus.queue_name
  password_message_ttl       = local.password_message_ttl_go_duration
  redis_host                 = module.redis.hostname
  redis_port                 = module.redis.tls_port
  redis_key_prefix           = var.redis_key_prefix
  sync_status_terminal_ttl   = local.sync_status_terminal_ttl_go_duration

  portal_allowed_cidrs = var.portal_allowed_cidrs
  trusted_proxy_cidrs  = var.trusted_proxy_cidrs
  rate_limit_per_ip    = var.rate_limit_per_ip
  rate_limit_window    = var.rate_limit_window

  entra_primary_domain       = var.entra_primary_domain
  entra_fallback_domain      = var.entra_fallback_domain
  graph_tenant_id            = var.graph_tenant_id
  graph_client_id            = var.graph_client_id
  password_encryption_key_id = var.password_encryption_key_id

  # Application Insights / observability values are provided by later work in Task 6.
  application_insights_retention_days = var.application_insights_retention_days
  log_analytics_name                  = local.log_analytics_name
  application_insights_name           = local.application_insights_name

  # Key Vault URI for KEY_VAULT_URL runtime env var.
  key_vault_uri = module.keyvault.vault_uri

  tags = local.common_tags

  # Explicit dependencies where role-assignment propagation, private endpoint
  # DNS creation, or Redis provisioning must complete before ACA reads them.
  depends_on = [
    module.network,
    module.servicebus,
    module.keyvault,
    module.redis,
  ]
}

