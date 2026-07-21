########################################
# Placeholder inputs for the Service Bus module.
#
# Task 3 replaces this stub with the real Premium namespace + queues,
# private endpoint, DNS records, and role assignments. Only the input
# surface consumed by the root module is declared here so `terraform
# init`/plan against Task 2 works before Task 3 lands.
########################################

variable "namespace_name" {
  description = "Service Bus namespace name (globally unique)."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
}

variable "resource_group_name" {
  description = "Application resource group."
  type        = string
}

variable "queue_name" {
  description = "Active password-sync queue name."
  type        = string
}

variable "safe_dead_letter_queue_name" {
  description = "Application safe DLQ queue name."
  type        = string
}

variable "message_ttl_iso8601" {
  description = "ISO-8601 duration for the active queue's message TTL (rendered from the shared numeric TTL)."
  type        = string
}

variable "runtime_identity_principal_id" {
  description = "Principal ID of the runtime UAMI for RBAC role assignments."
  type        = string
}

variable "private_endpoint_subnet_id" {
  description = "Subnet ID for the Service Bus private endpoint."
  type        = string
}

variable "private_dns_zone_ids" {
  description = "Map of Private DNS zone IDs (expects a service_bus key)."
  type        = map(string)
}

variable "tags" {
  description = "Common tags."
  type        = map(string)
  default     = {}
}
