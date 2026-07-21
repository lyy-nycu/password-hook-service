########################################
# Key Vault module
#
# Creates:
#   - Standard Key Vault (RBAC-only; no legacy access policies)
#       soft-delete retention 90 days, purge protection enabled,
#       public network access disabled unconditionally
#   - Private endpoint + DNS zone group in the approved subnet
#   - Key Vault Secrets User  → runtime UAMI (read-only secret access)
#   - Key Vault Secrets Officer → each explicitly supplied operator object ID
#
# IMPORTANT — deployment principal requirements:
#   Terraform's deployment principal must hold a role that grants
#   Microsoft.Authorization/roleAssignments/write at the vault scope
#   (e.g. Owner or User Access Administrator on the resource group)
#   before apply. Do NOT resolve insufficient permissions by weakening
#   the vault's access model or re-enabling public network access.
#
# NOTE: Azure RBAC role assignments can take several minutes to propagate.
########################################

resource "azurerm_key_vault" "this" {
  name                = var.vault_name
  location            = var.location
  resource_group_name = var.resource_group_name
  tenant_id           = var.tenant_id
  sku_name            = "standard"

  rbac_authorization_enabled    = true
  soft_delete_retention_days    = 90
  purge_protection_enabled      = true
  public_network_access_enabled = false

  tags = var.tags
}

resource "azurerm_private_endpoint" "keyvault" {
  name                = "pe-${var.vault_name}"
  location            = var.location
  resource_group_name = var.resource_group_name
  subnet_id           = var.private_endpoint_subnet_id

  private_service_connection {
    name                           = "psc-${var.vault_name}"
    private_connection_resource_id = azurerm_key_vault.this.id
    subresource_names              = ["vault"]
    is_manual_connection           = false
  }

  private_dns_zone_group {
    name                 = "dns-zone-group-keyvault"
    private_dns_zone_ids = [var.private_dns_zone_ids["key_vault"]]
  }

  tags = var.tags
}

# Runtime UAMI — read-only secret access only.
resource "azurerm_role_assignment" "secrets_user" {
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = var.runtime_identity_principal_id
}

# Named human operators — secret injection and rotation rights.
resource "azurerm_role_assignment" "secrets_officer" {
  for_each             = toset(var.operator_object_ids)
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = each.value
}
