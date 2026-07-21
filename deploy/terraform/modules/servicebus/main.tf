########################################
# Service Bus module
#
# Creates:
#   - Premium Service Bus namespace (local/SAS auth disabled, TLS 1.2+,
#     public network access disabled unconditionally)
#   - Active password-sync queue with ISO-8601 message TTL
#   - Application safe-DLQ queue (app-level queue, distinct from
#     Service Bus's broker-native dead-letter subqueue)
#   - Private endpoint + DNS zone group in the approved subnet
#   - Least-privilege RBAC for the runtime UAMI:
#       Azure Service Bus Data Sender  → active queue
#       Azure Service Bus Data Sender  → safe-DLQ queue
#       Azure Service Bus Data Receiver → active queue
#         (also authorises the ACA KEDA azure-servicebus scale rule)
#
# NOTE: Azure RBAC role assignments can take several minutes to propagate.
# Deployment verification must wait/retry rather than fall back to a
# connection string. local_auth_enabled = false enforces this at the
# namespace level.
########################################

resource "azurerm_servicebus_namespace" "this" {
  name                = var.namespace_name
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "Premium"

  # 1 messaging unit (capacity) and 1 message-partition at the namespace level.
  capacity                     = 1
  premium_messaging_partitions = 1

  local_auth_enabled            = false
  minimum_tls_version           = "1.2"
  public_network_access_enabled = false

  tags = var.tags
}

resource "azurerm_servicebus_queue" "active" {
  name         = var.queue_name
  namespace_id = azurerm_servicebus_namespace.this.id

  default_message_ttl = var.message_ttl_iso8601
}

resource "azurerm_servicebus_queue" "safe_dlq" {
  name         = var.safe_dead_letter_queue_name
  namespace_id = azurerm_servicebus_namespace.this.id
}

resource "azurerm_private_endpoint" "servicebus" {
  name                = "pe-${var.namespace_name}"
  location            = var.location
  resource_group_name = var.resource_group_name
  subnet_id           = var.private_endpoint_subnet_id

  private_service_connection {
    name                           = "psc-${var.namespace_name}"
    private_connection_resource_id = azurerm_servicebus_namespace.this.id
    subresource_names              = ["namespace"]
    is_manual_connection           = false
  }

  private_dns_zone_group {
    name                 = "dns-zone-group-servicebus"
    private_dns_zone_ids = [var.private_dns_zone_ids["service_bus"]]
  }

  tags = var.tags
}

# Sender on the active queue — app publishes password-sync jobs here.
resource "azurerm_role_assignment" "sender_active" {
  scope                = "${azurerm_servicebus_namespace.this.id}/queues/${azurerm_servicebus_queue.active.name}"
  role_definition_name = "Azure Service Bus Data Sender"
  principal_id         = var.runtime_identity_principal_id
}

# Sender on the safe-DLQ queue — app publishes safe-redacted payloads here.
resource "azurerm_role_assignment" "sender_safe_dlq" {
  scope                = "${azurerm_servicebus_namespace.this.id}/queues/${azurerm_servicebus_queue.safe_dlq.name}"
  role_definition_name = "Azure Service Bus Data Sender"
  principal_id         = var.runtime_identity_principal_id
}

# Receiver on the active queue — also authorises the ACA KEDA scale rule.
resource "azurerm_role_assignment" "receiver_active" {
  scope                = "${azurerm_servicebus_namespace.this.id}/queues/${azurerm_servicebus_queue.active.name}"
  role_definition_name = "Azure Service Bus Data Receiver"
  principal_id         = var.runtime_identity_principal_id
}
