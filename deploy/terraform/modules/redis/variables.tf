########################################
# Input variables for the Azure Managed Redis module.
########################################

variable "managed_redis_name" {
  description = "Managed Redis instance name (globally unique)."
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

variable "runtime_identity_principal_id" {
  description = "Principal ID of the runtime UAMI for the default access policy assignment."
  type        = string
}

variable "private_endpoint_subnet_id" {
  description = "Subnet ID for the Managed Redis private endpoint."
  type        = string
}

variable "private_dns_zone_ids" {
  description = "Map of Private DNS zone IDs (expects a managed_redis key)."
  type        = map(string)
}

variable "tags" {
  description = "Common tags."
  type        = map(string)
  default     = {}
}
