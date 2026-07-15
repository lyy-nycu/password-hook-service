########################################
# Key Vault module outputs
########################################

output "vault_id" {
  description = "Key Vault resource ID."
  value       = azurerm_key_vault.this.id
}

output "vault_name" {
  description = "Key Vault resource name."
  value       = azurerm_key_vault.this.name
}

output "vault_uri" {
  description = "Key Vault URI."
  value       = azurerm_key_vault.this.vault_uri
}

output "expected_secret_names" {
  description = "Non-secret metadata: names of secrets operators inject into Key Vault. Values are never stored in Terraform."
  value = {
    hmac_secret                     = "hook-hmac-secret"
    graph_client_secret             = "graph-client-secret"
    password_payload_encryption_key = "password-payload-encryption-key"
  }
}
