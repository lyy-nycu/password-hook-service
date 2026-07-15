########################################
# Placeholder inputs for the Key Vault module. Task 4 replaces this
# stub with the real RBAC-only Key Vault + private endpoint + role
# assignments. Input surface is declared here so the root wiring in
# Task 2 compiles.
########################################

variable "vault_name" {
  description = "Key Vault name (globally unique; 3-24 chars)."
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

variable "tenant_id" {
  description = "AAD tenant ID for the vault."
  type        = string
}

variable "runtime_identity_principal_id" {
  description = "Principal ID of the runtime UAMI for Key Vault Secrets User role assignment."
  type        = string
}

variable "private_endpoint_subnet_id" {
  description = "Subnet ID for the Key Vault private endpoint."
  type        = string
}

variable "private_dns_zone_ids" {
  description = "Map of Private DNS zone IDs (expects a key_vault key)."
  type        = map(string)
}

variable "tags" {
  description = "Common tags."
  type        = map(string)
  default     = {}
}
