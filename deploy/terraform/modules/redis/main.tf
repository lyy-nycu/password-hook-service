########################################
# Azure Managed Redis module
#
# Creates:
#   - Azure Managed Redis instance (Balanced_B0, HA enabled, OSSCluster,
#     NoEviction, access-key auth disabled, Encrypted client protocol,
#     public network access disabled unconditionally)
#   - Access policy assignment for the runtime UAMI (built-in default policy)
#   - Private endpoint + DNS zone group in the approved subnet
#
# Redis data is shared operational state (sync-status deduplication store),
# not an authoritative audit record. It survives app revisions and replica
# changes, but a Redis-loss fail-open can cause a duplicate Graph sync.
# Do not write password material, queue messages, HMAC material, Graph
# secrets, or raw UPNs to this instance.
#
# IMPORTANT — regional availability:
#   Azure Managed Redis availability, quota, and supported SKUs vary by
#   region. Confirm that Balanced_B0 is available in the target region
#   before running terraform apply. The root module passes japaneast via
#   var.location, which is a Managed Redis-supported region, but live
#   quota must still be verified in your subscription before apply.
########################################

resource "azurerm_managed_redis" "this" {
  name                = var.managed_redis_name
  resource_group_name = var.resource_group_name
  location            = var.location

  # Balanced_B0: plan-approved SKU. Verify Managed Redis quota and regional
  # availability in the target subscription/region before running apply.
  sku_name = "Balanced_B0"

  high_availability_enabled = true       # HA required by plan (also provider default; explicit for clarity)
  public_network_access     = "Disabled" # STRING enum, not a bool

  tags = var.tags

  default_database {
    access_keys_authentication_enabled = false        # Entra-only auth; disable access-key auth
    client_protocol                    = "Encrypted"  # TLS-encrypted client connections
    clustering_policy                  = "OSSCluster" # GA OSSCluster; NoCluster substitution is not permitted
    eviction_policy                    = "NoEviction" # must be set explicitly; provider default is VolatileLRU
  }
}

# Assign the built-in default data access policy to the runtime UAMI so that
# it can authenticate to the database via Entra (workload identity).
resource "azurerm_managed_redis_access_policy_assignment" "runtime" {
  managed_redis_id = azurerm_managed_redis.this.id
  object_id        = var.runtime_identity_principal_id
}

resource "azurerm_private_endpoint" "redis" {
  name                = "pe-${var.managed_redis_name}"
  location            = var.location
  resource_group_name = var.resource_group_name
  subnet_id           = var.private_endpoint_subnet_id

  private_service_connection {
    name                           = "psc-${var.managed_redis_name}"
    private_connection_resource_id = azurerm_managed_redis.this.id
    # "redisEnterprise" is the confirmed subresource name for azurerm_managed_redis,
    # inherited from the Redis Enterprise API namespace this resource supersedes.
    subresource_names    = ["redisEnterprise"]
    is_manual_connection = false
  }

  # Reference the existing managed_redis DNS zone created by the network module;
  # do NOT create a new zone or VNet link here.
  private_dns_zone_group {
    name                 = "dns-zone-group-redis"
    private_dns_zone_ids = [var.private_dns_zone_ids["managed_redis"]]
  }

  tags = var.tags
}
