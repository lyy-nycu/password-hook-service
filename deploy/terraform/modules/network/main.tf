########################################
# Network module
#
# Scope (approved by Task 0's private-network decisions):
#   - Create ONE dedicated private-endpoint subnet inside the existing
#     workload VNet supplied by the root.
#   - Create the Private DNS zones for Key Vault, Service Bus, and Azure
#     Managed Redis in the central network/DNS resource group and link
#     each zone to the existing workload VNet.
#
# Explicitly OUT OF SCOPE (do not add here):
#   - Virtual networks, VNet peerings, VPN gateways, GatewaySubnets,
#     Private DNS Resolver endpoints, or Application Gateways of any
#     kind. Those resources are owned by other repositories/pipelines.
########################################

resource "azurerm_subnet" "private_endpoints" {
  name                 = var.private_endpoint_subnet_name
  resource_group_name  = var.workload_vnet_resource_group_name
  virtual_network_name = var.workload_vnet_name
  address_prefixes     = [var.private_endpoint_subnet_cidr]

  # Task 0 requires no delegation and no service endpoints on this subnet.
  private_endpoint_network_policies             = "Disabled"
  private_link_service_network_policies_enabled = true
}

resource "azurerm_private_dns_zone" "this" {
  for_each = var.private_dns_zone_names

  name                = each.value
  resource_group_name = var.private_dns_zone_resource_group_name
  tags                = var.tags
}

resource "azurerm_private_dns_zone_virtual_network_link" "workload" {
  for_each = var.private_dns_zone_names

  name                  = "link-${each.key}-workload"
  resource_group_name   = var.private_dns_zone_resource_group_name
  private_dns_zone_name = azurerm_private_dns_zone.this[each.key].name
  virtual_network_id    = var.workload_vnet_id
  registration_enabled  = false
  tags                  = var.tags
}
