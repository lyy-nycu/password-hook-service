########################################
# Provider targeting
########################################

subscription_id = "56b72537-d985-4530-88f3-b6ed07e71c67"
tenant_id       = "7ef65350-5b77-4958-aca5-0ccadb6bd0b7"

########################################
# Environment, region, naming
########################################

environment = "stg"
location    = "japaneast"
name_prefix = "pwdhook"

########################################
# Application resource group
########################################

create_resource_group = true
resource_group_name   = "rg-password-hook-stg-jpe-001"

########################################
# Runtime identity
########################################

runtime_identity_name = "id-password-hook-stg"

########################################
# Existing consumed resources
########################################

existing_acr_resource_id = "/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-acr-jpe-001/providers/Microsoft.ContainerRegistry/registries/acrjpe001"

existing_container_app_environment_id = "/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-cae-stg-jpe-001/providers/Microsoft.App/managedEnvironments/cae-stg-jpe-001"

########################################
# Network mode and inputs
########################################

network_mode = "private_endpoints_in_existing_vnet"

existing_workload_vnet_id = "/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-spoke-paas/providers/Microsoft.Network/virtualNetworks/vnet-stg-jpe-001"

private_endpoint_subnet_name         = "snet-pe-password-hook-stg-jpe-001"
private_endpoint_subnet_cidr         = "10.0.4.96/27"
private_dns_zone_resource_group_name = "rg-spoke-paas"

# private_dns_zone_names left unset: the module default already matches
# staging (privatelink.vaultcore.azure.net / privatelink.servicebus.windows.net
# / privatelink.redis.azure.net).

########################################
# Application rollout gate
#
# app_image / app_image_tag deliberately NOT set here — Task 12's CD
# workflow always passes them via -var so the image tag can change every
# deploy without editing this file.
########################################

deploy_container_app = true

container_app_min_replicas = 1
container_app_max_replicas = 3

########################################
# Service Bus / password message TTL
########################################

service_bus_queue_name                  = "password-sync"
service_bus_safe_dead_letter_queue_name = "password-sync-safe-dlq"
password_message_ttl_seconds            = 300

########################################
# Redis sync-status retention
########################################

sync_status_terminal_ttl_days = 90
redis_key_prefix              = "password-hook:sync-status:"

########################################
# Observability
########################################

application_insights_retention_days = 90

########################################
# Portal source-address and rate limits
########################################

portal_allowed_cidrs = ["140.113.7.17/32", "10.0.8.4/32"]
trusted_proxy_cidrs  = ["100.100.0.0/16"]

rate_limit_per_ip = 10
rate_limit_window = "1m"

########################################
# Graph identifiers and password encryption key
########################################

entra_primary_domain       = "nycumis.onmicrosoft.com"
entra_fallback_domain      = ""
graph_tenant_id            = "7ef65350-5b77-4958-aca5-0ccadb6bd0b7"
graph_client_id            = "1d01a891-9214-4e38-a28e-52f068eb127b"
password_encryption_key_id = "password-payload-key-v1"

########################################
# Key Vault operator access
#
# The real staging Key Vault already grants Key Vault Secrets Officer to
# the human operator (lyy15@nycumis.onmicrosoft.com, object ID below) for
# manual secret injection/rotation (see deploy/terraform/README.md's
# "Inject secrets into Key Vault" step). This must be included here so
# CD's terraform plan/apply matches real state -- an empty list would
# destroy that operator's real, currently-granted access.
########################################

key_vault_operator_object_ids = ["7d37cec4-ad58-480c-aa10-a89d9190e412"]

########################################
# Application Gateway handoff (external owner pipeline manages the gateway)
########################################

private_api_hostname = "api.test.nycu.edu.tw"

application_gateway_resource_id = "/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-spoke-paas/providers/Microsoft.Network/applicationGateways/agw-stg-jpe-001"

application_gateway_listener_certificate_reference = "acme-api-test-nycu-edu-tw"
application_gateway_private_frontend_ip            = "10.0.8.62"

# application_gateway_listener_priority has no real Application Gateway
# counterpart (AGW listeners do not have a "priority" field — only routing
# rules do; verified via `az network application-gateway http-listener show`
# during plan-writing). It exists purely so the handoff-contract output
# (deploy/terraform/application-gateway-handoff.md) can echo a requested
# value; set equal to the real rule priority below for consistency.
application_gateway_listener_priority = 120
application_gateway_rule_priority     = 120

application_gateway_backend_probe_path = "/healthz"

# application_gateway_waf_policy left unset: the module default (Prevention,
# OWASP 3.2 + BotManagerRuleSet 0.1, BlockNonPortalSources priority 10)
# already matches the real deployed staging policy.
