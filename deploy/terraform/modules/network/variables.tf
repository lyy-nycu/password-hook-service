variable "environment" {
  description = "Deployment environment (used only for tagging/labelling)."
  type        = string
}

variable "location" {
  description = "Azure region of the created resources (private-endpoint subnet). Private DNS zones are global; the value is ignored for them."
  type        = string
}

variable "private_endpoint_subnet_name" {
  description = "Name of the dedicated private-endpoint subnet to create inside the existing workload VNet."
  type        = string
}

variable "private_endpoint_subnet_cidr" {
  description = "CIDR of the dedicated private-endpoint subnet. Must be inside the existing workload VNet's address space and not overlap with existing subnets."
  type        = string
}

variable "workload_vnet_id" {
  description = "Resource ID of the existing workload VNet the private-endpoint subnet is created in and the Private DNS zones are linked to."
  type        = string
}

variable "workload_vnet_name" {
  description = "Name of the existing workload VNet (parsed from workload_vnet_id by the root)."
  type        = string
}

variable "workload_vnet_resource_group_name" {
  description = "Resource group of the existing workload VNet (parsed from workload_vnet_id by the root)."
  type        = string
}

variable "private_dns_zone_resource_group_name" {
  description = "Resource group that owns the Private DNS zones. Task 0 selects the central network/DNS RG."
  type        = string
}

variable "private_dns_zone_names" {
  description = "Private DNS zone names for Key Vault, Service Bus, and Managed Redis."
  type = object({
    key_vault     = string
    service_bus   = string
    managed_redis = string
  })
}

variable "tags" {
  description = "Common tags applied to created resources."
  type        = map(string)
  default     = {}
}
