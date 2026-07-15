########################################
# Azure Container Apps module
#
# Creates:
#   - Log Analytics workspace and workspace-based Application Insights
#     component owned by this deployment.
#   - Managed OpenTelemetry agent configuration patched onto the
#     externally-owned ACA managed environment via azapi_update_resource
#     (traces + logs -> Application Insights). We never create or replace
#     the environment; we only merge-PATCH the openTelemetryConfiguration
#     property, so the environment's own appLogsConfiguration/Log Analytics
#     wiring stays exactly as its owner set it.
#   - AcrPull role assignment for the runtime UAMI at the approved
#     existing ACR scope so identity-based image pulls work.
#   - Internal Container App (gated by var.deploy_container_app) with the
#     runtime UAMI attached, identity-based registry auth, single-revision
#     internal HTTPS ingress on target port 8080, and Service Bus
#     queue-length KEDA scaling.
#   - Monitoring Metrics Publisher role assignment for the runtime UAMI
#     at the Container App scope so the custom-metrics exporter can write
#     to Azure Monitor.
#
# IMPORTANT — no Application Gateway:
#   The shared Application Gateway is owned and reconciled by the
#   external lyy-nycu/ldap-service pipeline. This module MUST NOT declare
#   or import azurerm_application_gateway. See
#   deploy/terraform/application-gateway-handoff.md for the operator
#   contract the AGW owner consumes.
#
# IMPORTANT — no secret values:
#   No connection strings, access keys, HMAC values, Graph client secret
#   values, or encryption-key material are ever passed here. Only Key
#   Vault URIs and secret names (references, not values) flow through.
########################################

terraform {
  required_providers {
    azurerm = {
      source = "hashicorp/azurerm"
    }
    azapi = {
      source = "Azure/azapi"
    }
  }
}

data "azurerm_client_config" "current" {}

resource "azurerm_log_analytics_workspace" "this" {
  name                = var.log_analytics_name
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "PerGB2018"
  retention_in_days   = var.application_insights_retention_days

  tags = var.tags
}

resource "azurerm_application_insights" "this" {
  name                = var.application_insights_name
  location            = var.location
  resource_group_name = var.resource_group_name
  # "other" fits a backend service. "web" targets browser SDKs and would
  # advertise browser-specific instrumentation this service does not use.
  application_type  = "other"
  workspace_id      = azurerm_log_analytics_workspace.this.id
  retention_in_days = var.application_insights_retention_days

  tags = var.tags
}

########################################
# Managed OpenTelemetry agent — merge-PATCH the existing ACA environment
#
# API version 2025-07-01 is a stable GA version that still exposes the
# openTelemetryConfiguration property; it is @removed in 2026-01-01, so
# we pin explicitly. appInsightsConfiguration is a sibling of
# openTelemetryConfiguration under properties; metrics are intentionally
# not listed here because the App Insights destination supports only
# logs and traces via the managed agent. Custom metrics reach Azure
# Monitor through the separate custom-metrics exporter this module also
# wires (see the Monitoring Metrics Publisher role assignment below).
########################################

resource "azapi_update_resource" "otel_agent" {
  type        = "Microsoft.App/managedEnvironments@2025-07-01"
  resource_id = var.container_app_environment_id

  body = {
    properties = {
      appInsightsConfiguration = {
        connectionString = azurerm_application_insights.this.connection_string
      }
      openTelemetryConfiguration = {
        tracesConfiguration = {
          destinations = ["appInsights"]
        }
        logsConfiguration = {
          destinations = ["appInsights"]
        }
      }
    }
  }
}

########################################
# AcrPull for the runtime UAMI at the approved existing ACR scope.
# Must exist before the Container App attempts an identity-based pull.
########################################

resource "azurerm_role_assignment" "acr_pull" {
  scope                = var.existing_acr_resource_id
  role_definition_name = "AcrPull"
  principal_id         = var.runtime_identity_principal_id
}

########################################
# Custom-metrics resource ID
#
# The brief requires this to be a stable ARM ID derived from
# subscription/resource group/app name, NOT a self-reference to the
# Container App resource. Building it this way keeps AZURE_MONITOR_
# METRIC_RESOURCE_ID knowable even when deploy_container_app is false.
########################################

locals {
  metrics_resource_id = "/subscriptions/${data.azurerm_client_config.current.subscription_id}/resourceGroups/${var.resource_group_name}/providers/Microsoft.App/containerApps/${var.container_app_name}"

  # KEDA azure-servicebus scaler expects the bare namespace name
  # (no ".servicebus.windows.net" suffix) in its metadata.
  service_bus_namespace_name = replace(var.service_bus_namespace_fqdn, ".servicebus.windows.net", "")
}

resource "azurerm_role_assignment" "metrics_publisher" {
  count = var.deploy_container_app ? 1 : 0

  scope                = local.metrics_resource_id
  role_definition_name = "Monitoring Metrics Publisher"
  principal_id         = var.runtime_identity_principal_id
}

########################################
# Container App
########################################

resource "azurerm_container_app" "this" {
  count = var.deploy_container_app ? 1 : 0

  name                         = var.container_app_name
  resource_group_name          = var.resource_group_name
  container_app_environment_id = var.container_app_environment_id
  revision_mode                = "Single"
  tags                         = var.tags

  identity {
    type         = "UserAssigned"
    identity_ids = [var.runtime_identity_id]
  }

  # Identity-based ACR authentication. Mutually exclusive with
  # username/password_secret_name — do not add either.
  registry {
    server   = var.existing_acr_login_server
    identity = var.runtime_identity_id
  }

  # Internal-only ingress. external_enabled MUST stay false: the
  # environment itself is internal-only and portal callers reach this
  # backend exclusively through the shared Application Gateway.
  ingress {
    external_enabled           = false
    target_port                = 8080
    transport                  = "http"
    allow_insecure_connections = false

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  template {
    min_replicas = var.min_replicas
    max_replicas = var.max_replicas

    container {
      name = "password-hook-service"
      # var.image is validated at root to (a) come from the approved ACR
      # login server and (b) carry a tag equal to var.image_tag, so we
      # use it verbatim rather than reconstructing the reference here.
      image  = var.image
      cpu    = 0.5
      memory = "1Gi"

      liveness_probe {
        transport = "HTTP"
        path      = "/healthz"
        port      = 8080
      }
      readiness_probe {
        transport = "HTTP"
        path      = "/healthz"
        port      = 8080
      }
      startup_probe {
        transport = "HTTP"
        path      = "/healthz"
        port      = 8080
      }

      # ---- Secret loading (non-secret references) ----
      env {
        name  = "SECRETS_SOURCE"
        value = "keyvault"
      }
      env {
        name  = "KEY_VAULT_URL"
        value = var.key_vault_uri
      }
      env {
        name  = "KEY_VAULT_HMAC_SECRET_NAME"
        value = var.key_vault_secret_names.hmac_secret
      }
      env {
        name  = "KEY_VAULT_GRAPH_CLIENT_SECRET_NAME"
        value = var.key_vault_secret_names.graph_client_secret
      }
      env {
        name  = "KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME"
        value = var.key_vault_secret_names.password_payload_encryption_key
      }

      # ---- Service Bus (managed identity, no connection string) ----
      env {
        name  = "SERVICEBUS_AUTH_MODE"
        value = "managed_identity"
      }
      env {
        name  = "SERVICEBUS_NAMESPACE_FQDN"
        value = var.service_bus_namespace_fqdn
      }
      env {
        name  = "SERVICEBUS_QUEUE_NAME"
        value = var.service_bus_queue_name
      }
      env {
        name  = "PASSWORD_MESSAGE_TTL"
        value = var.password_message_ttl
      }

      # ---- Sync status (Redis, Entra auth, no access keys) ----
      env {
        name  = "SYNC_STATUS_STORE"
        value = "redis"
      }
      env {
        name  = "REDIS_HOST"
        value = var.redis_host
      }
      env {
        name  = "REDIS_PORT"
        value = tostring(var.redis_port)
      }
      env {
        name  = "REDIS_KEY_PREFIX"
        value = var.redis_key_prefix
      }
      env {
        name  = "SYNC_STATUS_TERMINAL_TTL"
        value = var.sync_status_terminal_ttl
      }

      # ---- Workload identity ----
      env {
        name  = "AZURE_CLIENT_ID"
        value = var.runtime_identity_client_id
      }

      # ---- Portal enforcement / trusted proxies / rate limits ----
      # internal/config/config.go parses PORTAL_ALLOWED_CIDRS and
      # TRUSTED_PROXY_CIDRS as comma-separated CIDR lists (csvEnv).
      env {
        name  = "PORTAL_ALLOWED_CIDRS"
        value = join(",", var.portal_allowed_cidrs)
      }
      env {
        name  = "TRUSTED_PROXY_CIDRS"
        value = join(",", var.trusted_proxy_cidrs)
      }
      env {
        name  = "RATE_LIMIT_PER_IP"
        value = tostring(var.rate_limit_per_ip)
      }
      env {
        name  = "RATE_LIMIT_WINDOW"
        value = var.rate_limit_window
      }

      # ---- Domains / Graph / password key ID ----
      env {
        name  = "ENTRA_PRIMARY_DOMAIN"
        value = var.entra_primary_domain
      }
      env {
        name  = "ENTRA_FALLBACK_DOMAIN"
        value = var.entra_fallback_domain
      }
      env {
        name  = "GRAPH_TENANT_ID"
        value = var.graph_tenant_id
      }
      env {
        name  = "GRAPH_CLIENT_ID"
        value = var.graph_client_id
      }
      env {
        name  = "PASSWORD_ENCRYPTION_KEY_ID"
        value = var.password_encryption_key_id
      }

      # ---- Observability ----
      # OTEL_EXPORTER_OTLP_ENDPOINT is INTENTIONALLY absent: the ACA
      # managed OpenTelemetry agent injects it automatically. Setting
      # it explicitly here would compete with that injection.
      env {
        name  = "OBSERVABILITY_EXPORTER"
        value = "azure_monitor"
      }
      env {
        name  = "AZURE_MONITOR_METRIC_RESOURCE_ID"
        value = local.metrics_resource_id
      }
      env {
        name  = "AZURE_MONITOR_METRIC_REGION"
        value = var.location
      }
    }

    # KEDA azure-servicebus scaler. Identity-based (identity_id points
    # at the runtime UAMI) so no scaler secret/connection string is
    # ever required. queueName + bare namespace + messageCount match
    # KEDA's documented azure-servicebus metadata schema.
    custom_scale_rule {
      name             = "servicebus-queue-scaler"
      custom_rule_type = "azure-servicebus"
      metadata = {
        queueName    = var.service_bus_queue_name
        namespace    = local.service_bus_namespace_name
        messageCount = "50"
      }
      identity_id = var.runtime_identity_id
    }
  }

  # Ensure AcrPull propagation happens before the app tries to pull.
  depends_on = [
    azurerm_role_assignment.acr_pull,
    azapi_update_resource.otel_agent,
  ]
}
