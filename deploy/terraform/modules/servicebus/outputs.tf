########################################
# Placeholder outputs for the Service Bus module.
#
# Task 3 replaces these `null` values with real Service Bus attributes.
# The root outputs.tf and ACA module wiring already consume these names,
# so Task 3 must preserve the output names/types.
########################################

output "namespace_id" {
  description = "Service Bus namespace resource ID (populated by Task 3)."
  value       = null
}

output "namespace_fqdn" {
  description = "Service Bus namespace FQDN (populated by Task 3)."
  value       = null
}

output "queue_name" {
  description = "Active queue name (echoes the input; populated by Task 3)."
  value       = var.queue_name
}

output "safe_dead_letter_queue_name" {
  description = "Application safe-DLQ queue name (echoes the input; populated by Task 3)."
  value       = var.safe_dead_letter_queue_name
}
