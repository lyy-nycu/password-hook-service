output "private_endpoint_subnet_id" {
  description = "Resource ID of the dedicated private-endpoint subnet."
  value       = azurerm_subnet.private_endpoints.id
}

output "private_dns_zone_ids" {
  description = "Map of Private DNS zone IDs keyed by service (key_vault, service_bus, managed_redis)."
  value       = { for k, z in azurerm_private_dns_zone.this : k => z.id }
}

output "private_dns_zone_names" {
  description = "Map of Private DNS zone names keyed by service."
  value       = { for k, z in azurerm_private_dns_zone.this : k => z.name }
}
