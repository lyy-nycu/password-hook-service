########################################
# Outputs for the Azure Managed Redis module.
# Names are fixed — the root module and ACA env wiring (Task 6) consume
# hostname (→ REDIS_HOST) and tls_port (→ REDIS_PORT) by these exact names.
########################################

output "hostname" {
  description = "Managed Redis hostname (REDIS_HOST)."
  value       = azurerm_managed_redis.this.hostname
}

output "tls_port" {
  description = "Managed Redis TLS port (REDIS_PORT)."
  value       = azurerm_managed_redis.this.default_database[0].port
}

output "resource_id" {
  description = "Managed Redis resource ID."
  value       = azurerm_managed_redis.this.id
}
