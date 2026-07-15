########################################
# Service Bus module outputs
########################################

output "namespace_id" {
  description = "Service Bus namespace resource ID."
  value       = azurerm_servicebus_namespace.this.id
}

output "namespace_fqdn" {
  description = "Service Bus namespace FQDN (used by ACA as SERVICEBUS_NAMESPACE_FQDN)."
  value       = "${azurerm_servicebus_namespace.this.name}.servicebus.windows.net"
}

output "queue_name" {
  description = "Active queue name."
  value       = azurerm_servicebus_queue.active.name
}

output "safe_dead_letter_queue_name" {
  description = "Application safe-DLQ queue name."
  value       = azurerm_servicebus_queue.safe_dlq.name
}

output "queue_id" {
  description = "Active queue resource ID (used by ACA for scale-rule role-assignment ordering)."
  value       = azurerm_servicebus_queue.active.id
}

output "safe_dead_letter_queue_id" {
  description = "Application safe-DLQ queue resource ID."
  value       = azurerm_servicebus_queue.safe_dlq.id
}
