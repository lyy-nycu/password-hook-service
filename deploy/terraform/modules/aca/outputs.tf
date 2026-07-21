########################################
# ACA module outputs
########################################

output "container_app_id" {
  description = "Container App resource ID; null when var.deploy_container_app is false."
  value       = try(azurerm_container_app.this[0].id, null)
}

output "container_app_backend_fqdn" {
  description = "ACA ingress FQDN used by the Application Gateway backend pool (SNI + host header). Resolves to the actual ingress FQDN once the Container App exists (Pass 2), and to a deterministic prediction '<container-app-name>.<managed-environment-default-domain>' beforehand so the application_gateway_handoff output has a real value on Pass 1. Both values are the same hostname Azure assigns to an external_enabled=true Container App. external_enabled MUST stay true here: Azure's 'internal' ingress is scoped to app-to-app calls within the same ACA environment and is not reachable from the Application Gateway, even though the environment's own VNet configuration keeps this hostname unreachable from the public internet."
  value = try(
    azurerm_container_app.this[0].ingress[0].fqdn,
    "${var.container_app_name}.${var.container_app_environment_default_domain}"
  )
}

output "log_analytics_id" {
  description = "Log Analytics workspace resource ID."
  value       = azurerm_log_analytics_workspace.this.id
}

output "application_insights_id" {
  description = "Application Insights component resource ID."
  value       = azurerm_application_insights.this.id
}

output "azure_monitor_metrics_resource_id" {
  description = "Custom-metrics resource ID (Container App) for the Azure Monitor exporter. Constructed from subscription/RG/app name so it is stable even before deploy_container_app is true."
  value       = local.metrics_resource_id
}
