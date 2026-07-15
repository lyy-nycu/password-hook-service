########################################
# Placeholder outputs for the Azure Managed Redis module. Task 5 replaces
# the `null` values with real Managed Redis attributes. The output
# names/types must be preserved so the root and ACA module wiring keep
# compiling.
########################################

output "hostname" {
  description = "Managed Redis hostname (REDIS_HOST). Populated by Task 5."
  value       = null
}

output "tls_port" {
  description = "Managed Redis TLS port (REDIS_PORT). Populated by Task 5."
  value       = null
}

output "resource_id" {
  description = "Managed Redis resource ID. Populated by Task 5."
  value       = null
}
