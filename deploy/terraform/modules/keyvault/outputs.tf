########################################
# Placeholder outputs for the Key Vault module. Task 4 replaces the
# `null` values with real vault attributes; the output names/types
# must be preserved.
########################################

output "vault_id" {
  description = "Key Vault resource ID (populated by Task 4)."
  value       = null
}

output "vault_uri" {
  description = "Key Vault URI (populated by Task 4)."
  value       = null
}

output "expected_secret_names" {
  description = "Non-secret metadata: names of secrets operators inject into Key Vault. Values are never stored in Terraform."
  value = {
    hmac_secret                     = "hook-hmac-secret"
    graph_client_secret             = "graph-client-secret"
    password_payload_encryption_key = "password-payload-encryption-key"
  }
}
