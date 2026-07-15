########################################
# ACA module outputs
########################################

output "container_app_id" {
  description = "Container App resource ID; null when var.deploy_container_app is false."
  value       = try(azurerm_container_app.this[0].id, null)
}

output "container_app_backend_fqdn" {
  description = "Internal ACA ingress FQDN used by the Application Gateway backend pool (SNI + host header); null when var.deploy_container_app is false."
  value       = try(azurerm_container_app.this[0].ingress[0].fqdn, null)
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
