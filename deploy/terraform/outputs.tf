########################################
# Runtime identity
########################################

output "runtime_identity_id" {
  description = "Resource ID of the user-assigned runtime identity."
  value       = azurerm_user_assigned_identity.runtime.id
}

output "runtime_identity_client_id" {
  description = "Client ID of the user-assigned runtime identity (safe to publish; used as AZURE_CLIENT_ID)."
  value       = azurerm_user_assigned_identity.runtime.client_id
}

output "runtime_identity_principal_id" {
  description = "Principal (object) ID of the user-assigned runtime identity (safe to publish; used for role assignments)."
  value       = azurerm_user_assigned_identity.runtime.principal_id
}

########################################
# Existing consumed resources (echoed as operator-safe metadata)
########################################

output "existing_acr_login_server" {
  description = "Login server of the approved existing ACR (image push/pull target)."
  value       = data.azurerm_container_registry.existing.login_server
}

output "existing_acr_resource_id" {
  description = "Resource ID of the approved existing ACR."
  value       = var.existing_acr_resource_id
}

output "existing_container_app_environment_id" {
  description = "Resource ID of the existing ACA managed environment consumed by the Container App."
  value       = var.existing_container_app_environment_id
}

output "container_app_environment_static_ingress_ip" {
  description = "Static internal ingress IP of the existing ACA environment (used by the Application Gateway backend pool)."
  value       = data.azurerm_container_app_environment.existing.static_ip_address
}

########################################
# Module identifiers (values populated once module tasks complete)
########################################

output "key_vault_id" {
  description = "Key Vault resource ID."
  value       = module.keyvault.vault_id
}

output "key_vault_uri" {
  description = "Key Vault URI (KEY_VAULT_URL)."
  value       = module.keyvault.vault_uri
}

output "expected_key_vault_secret_names" {
  description = "Non-secret metadata: the secret names operators inject into Key Vault. Values are never in Terraform."
  value       = module.keyvault.expected_secret_names
}

output "service_bus_namespace_id" {
  description = "Service Bus namespace resource ID."
  value       = module.servicebus.namespace_id
}

output "service_bus_namespace_fqdn" {
  description = "Service Bus namespace FQDN (SERVICEBUS_NAMESPACE_FQDN)."
  value       = module.servicebus.namespace_fqdn
}

output "service_bus_queue_name" {
  description = "Active password-sync queue name."
  value       = module.servicebus.queue_name
}

output "service_bus_safe_dead_letter_queue_name" {
  description = "Application safe-DLQ queue name (distinct from broker-native DLQ)."
  value       = module.servicebus.safe_dead_letter_queue_name
}

output "managed_redis_hostname" {
  description = "Azure Managed Redis hostname (REDIS_HOST)."
  value       = module.redis.hostname
}

output "managed_redis_tls_port" {
  description = "Azure Managed Redis TLS port (REDIS_PORT)."
  value       = module.redis.tls_port
}

output "managed_redis_resource_id" {
  description = "Azure Managed Redis resource ID."
  value       = module.redis.resource_id
}

output "container_app_id" {
  description = "Container App resource ID (null until deploy_container_app is true)."
  value       = module.aca.container_app_id
}

output "container_app_backend_fqdn" {
  description = "Internal ACA backend FQDN for the Application Gateway backend pool."
  value       = module.aca.container_app_backend_fqdn
}

output "application_insights_id" {
  description = "Application Insights resource ID."
  value       = module.aca.application_insights_id
}

output "log_analytics_id" {
  description = "Log Analytics workspace resource ID."
  value       = module.aca.log_analytics_id
}

output "azure_monitor_metrics_resource_id" {
  description = "Custom-metrics resource ID (Container App) used by the Azure Monitor exporter."
  value       = module.aca.azure_monitor_metrics_resource_id
}

########################################
# Network / DNS resource IDs (created here)
########################################

output "private_endpoint_subnet_id" {
  description = "Resource ID of the dedicated private-endpoint subnet created by this configuration."
  value       = module.network.private_endpoint_subnet_id
}

output "private_dns_zone_ids" {
  description = "Resource IDs of the Private DNS zones this configuration created (Key Vault, Service Bus, Managed Redis)."
  value       = module.network.private_dns_zone_ids
}

########################################
# Application Gateway handoff contract
#
# This repository does NOT manage the shared Application Gateway. The
# external owner pipeline (lyy-nycu/ldap-service) implements the private
# frontend/listener/rule/WAF-policy additions using these requested
# values. The listener/rule/frontend IDs are only knowable after the
# external pipeline completes; do not claim them as managed here.
########################################

output "application_gateway_handoff" {
  description = "Operator-safe handoff contract for the external Application Gateway owner pipeline. All values are requested inputs plus values this deployment can safely publish; none are secrets."
  value = {
    application_gateway_resource_id   = var.application_gateway_resource_id
    private_api_hostname              = var.private_api_hostname
    requested_private_frontend_ip     = var.application_gateway_private_frontend_ip
    requested_listener_priority       = var.application_gateway_listener_priority
    requested_rule_priority           = var.application_gateway_rule_priority
    requested_waf_block_rule_priority = var.application_gateway_waf_block_rule_priority
    listener_certificate_reference    = var.application_gateway_listener_certificate_reference
    backend_fqdn                      = module.aca.container_app_backend_fqdn
    backend_sni                       = module.aca.container_app_backend_fqdn
    backend_host_header               = module.aca.container_app_backend_fqdn
    backend_probe_path                = var.application_gateway_backend_probe_path
    approved_portal_source_cidrs      = var.portal_allowed_cidrs
    waf_policy_mode                   = "Prevention"
    waf_managed_rule_sets             = ["OWASP 3.2", "BotManager 0.1"]
    note                              = "Listener/rule/frontend IDs are not published by this Terraform. The external owner pipeline must record them separately after apply."
  }
}

