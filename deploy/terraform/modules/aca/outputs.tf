########################################
# Placeholder outputs for the ACA / Container App module. Task 6 replaces
# these `null` values with real Container App / Log Analytics /
# Application Insights attributes; output names/types must be preserved.
########################################

output "container_app_id" {
  description = "Container App resource ID (populated by Task 6 when deploy_container_app is true)."
  value       = null
}

output "container_app_backend_fqdn" {
  description = "Container App internal backend FQDN used by the Application Gateway backend pool (populated by Task 6)."
  value       = null
}

output "log_analytics_id" {
  description = "Log Analytics workspace resource ID (populated by Task 6)."
  value       = null
}

output "application_insights_id" {
  description = "Application Insights component resource ID (populated by Task 6)."
  value       = null
}

output "azure_monitor_metrics_resource_id" {
  description = "Custom-metrics resource ID for the Azure Monitor exporter (populated by Task 6)."
  value       = null
}
