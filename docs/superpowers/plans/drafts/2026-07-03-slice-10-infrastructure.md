# Slice 10 Infrastructure Draft Implementation Plan

> **Status:** Draft. This plan is a future-slice planning artifact only. Do not execute it until Slice 7 is merged, Slice 8 and Slice 9 are refreshed or implemented as needed, this draft is refreshed against `main`, provider documentation is rechecked, and the plan is promoted to `docs/superpowers/plans/active/`.
>
> **For agentic workers:** REQUIRED SUB-SKILL WHEN PROMOTED: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provision deployable Azure infrastructure for the password hook service: Azure Container Apps, Azure Container Registry, Service Bus queues, Key Vault, managed identities, secret access, and queue-depth scaling rules matching the application design.

**Architecture:** Use Terraform to compose focused modules for ACA/ACR, Service Bus, and Key Vault from `deploy/terraform/`. Keep secret values out of Terraform state; Terraform creates resource boundaries, identities, access grants, queue names, and non-secret app configuration, while operators inject actual HMAC, Graph, Service Bus, and password-encryption secrets through a documented post-provision path. Keep application behavior unchanged in this slice except for infrastructure-driven environment variables.

**Tech Stack:** Terraform `>= 1.8.0`, AzureRM provider, Azure Container Apps, Azure Container Registry, Azure Service Bus Standard, Azure Key Vault, Log Analytics workspace, user-assigned managed identity, existing distroless Docker image.

---

## Draft Constraints

- Draft only. Do not implement this plan until it is refreshed and promoted to `active/`.
- Do not update `docs/superpowers/plans/README.md`, `docs/superpowers/plans/roadmap.md`, or any active plan pointer while this remains a draft.
- Refresh this draft after Slice 8 and Slice 9 because observability and API protection may add required environment variables, allowed CIDRs, metrics settings, or ingress assumptions.
- Recheck current AzureRM provider documentation when promoting this plan. This draft records intended resource shape, not a guarantee that provider block syntax is still current.
- Promotion must add Azure naming validation or deterministic suffixes for globally constrained resources such as ACR, Key Vault, and Service Bus namespace names. Do not rely on raw `name_prefix` values being unique or length-safe.
- Do not put real secret values in Terraform variables, `.tfvars`, plan output, state, or logs.
- Do not create `azurerm_key_vault_secret` resources for HMAC secret, Service Bus connection string, Graph client secret, or password payload encryption key unless the owner explicitly accepts the state exposure tradeoff. Default path is operator-managed secret injection after Terraform apply.
- Do not create Terraform-managed Service Bus SAS authorization rules by default. Their keys and connection strings are stored in Terraform state. If the application still requires a Service Bus connection string at promotion time, create the required SAS credential outside Terraform and inject it into Key Vault.
- Do not add CI/CD pipeline, scanner gates, staging smoke tests, alert rules, dashboards, or production runbooks in this slice; those belong to Slice 11 and Slice 12 unless explicitly requested.
- Do not implement WAF, Azure Front Door, private endpoints, VPN routing, DNS cutover, or Azure DDoS Protection in this slice unless the owner expands Slice 10 scope. Slice 10 should leave clean outputs and variables for those later infrastructure controls.

## Current Context

- `deploy/terraform/main.tf`, `variables.tf`, and `outputs.tf` are placeholders.
- `deploy/terraform/modules/aca/main.tf`, `modules/servicebus/main.tf`, and `modules/keyvault/main.tf` are placeholders.
- `deploy/Dockerfile` already builds a distroless static Linux container exposing port `8080`.
- The runtime currently supports `SECRETS_SOURCE=keyvault`, `KEY_VAULT_URL`, Key Vault secret name variables, Service Bus queue names, Graph tenant/client IDs, and password encryption key ID.
- The app currently loads the Service Bus connection string and Graph client secret as secrets. Terraform must not store those secret values by default.
- The source design calls for ACA ingress with TLS termination, Service Bus queue depth scaling at 50 messages per replica, 0.5 vCPU, 1 GiB memory, Service Bus Standard, Key Vault, and Azure Monitor/Log Analytics integration.
- Slice 9 draft says application-level source allowlist and rate settings may become required config. Slice 10 should expose variables for the portal web-server egress CIDRs and rate settings, but should not implement WAF/Front Door enforcement.
- Promotion must verify what client source address the Go app sees behind Azure Container Apps ingress. If `r.RemoteAddr` is the ACA proxy address instead of the portal web-server egress IP, reconcile Slice 9 source allowlist behavior before executing this infrastructure plan.

## Infrastructure Story

- Terraform creates the infrastructure shell: resource group targeting, Log Analytics workspace, ACR, Container Apps environment, user-assigned identity, Container App, Service Bus namespace and queues, Key Vault, role assignments or access policies, and non-secret configuration.
- Terraform should support both existing and newly created resource groups. The recommended default is creating or managing a dedicated resource group named from `name_prefix` and `environment`.
- The Container App runs the single existing binary. It starts the HTTP server and worker in the same container, matching current `app.New` behavior.
- Service Bus has one active password sync queue and one application-level safe DLQ queue. Native Service Bus DLQ remains broker behavior, but application terminal failures are sent to the safe DLQ queue.
- Key Vault stores runtime secrets, but real secret values are injected outside Terraform. The Terraform output should provide exact secret names and an Azure CLI command template for operators.
- The Container App's managed identity can read Key Vault secrets. If the app still requires a Service Bus connection string after refresh, operators create the credential outside Terraform and inject it into Key Vault. If the app supports Managed Identity for Service Bus by promotion time, prefer RBAC over connection strings and update this plan.
- ACA queue-depth scaling watches the active Service Bus queue and scales replicas based on the design threshold of 50 active messages per replica.
- Container image bootstrap is a two-step operational concern: ACR must exist before the first image can be pushed, and the Container App must reference an image tag that exists. The promoted plan must either document an ACR-first apply/push/apply sequence or accept an existing image registry for the first deployment.

## File Structure

- Modify `deploy/terraform/main.tf`: provider configuration, root module composition, locals, resource group selection, and module wiring.
- Modify `deploy/terraform/variables.tf`: typed variables for naming, location, image, app config, queue names, Key Vault secret names, scaling, and tags.
- Modify `deploy/terraform/outputs.tf`: app URL, ACR login server, Key Vault URI/name, identity principal/client IDs, Service Bus namespace/queue names, and secret injection command templates.
- Modify `deploy/terraform/modules/servicebus/main.tf`: Service Bus namespace, active queue, and safe DLQ queue.
- Create `deploy/terraform/modules/servicebus/variables.tf`: module inputs.
- Create `deploy/terraform/modules/servicebus/outputs.tf`: namespace, queue, and DLQ outputs.
- Modify `deploy/terraform/modules/keyvault/main.tf`: Key Vault, tenant-aware access model, purge protection/soft delete, managed identity read grants, optional operator principal grants.
- Create `deploy/terraform/modules/keyvault/variables.tf`: module inputs.
- Create `deploy/terraform/modules/keyvault/outputs.tf`: vault URI/name and configured secret names.
- Modify `deploy/terraform/modules/aca/main.tf`: Log Analytics workspace, ACR, user-assigned managed identity, Container Apps environment, Container App, ingress, env vars, secrets references, and scale rules.
- Create `deploy/terraform/modules/aca/variables.tf`: module inputs.
- Create `deploy/terraform/modules/aca/outputs.tf`: app FQDN/URL, ACR login server, identity IDs, and environment IDs.
- Create `deploy/terraform/README.md`: local Terraform workflow, secret injection workflow, state-safety rules, and deployment checklist.
- Create `deploy/terraform/examples/staging.tfvars.example`: non-secret example values only.
- Modify root `README.md`: link to Terraform README and clarify infrastructure remains draft until promoted.

---

### Task 1: Root Terraform Shape And Variables

**Files:**
- Modify: `deploy/terraform/main.tf`
- Modify: `deploy/terraform/variables.tf`
- Modify: `deploy/terraform/outputs.tf`
- Create: `deploy/terraform/examples/staging.tfvars.example`

- [ ] **Step 1: Replace root placeholder with provider and module skeleton**

Edit `deploy/terraform/main.tf`:

```hcl
terraform {
  required_version = ">= 1.8.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}

provider "azurerm" {
  features {}
}

locals {
  name        = "${var.name_prefix}-${var.environment}"
  common_tags = merge(var.tags, {
    application = "password-hook-service"
    environment = var.environment
  })
}

resource "azurerm_resource_group" "this" {
  count    = var.create_resource_group ? 1 : 0
  name     = var.resource_group_name
  location = var.location
  tags     = local.common_tags
}

data "azurerm_resource_group" "this" {
  count = var.create_resource_group ? 0 : 1
  name  = var.resource_group_name
}

locals {
  resource_group_name     = var.create_resource_group ? azurerm_resource_group.this[0].name : data.azurerm_resource_group.this[0].name
  resource_group_location = var.create_resource_group ? azurerm_resource_group.this[0].location : data.azurerm_resource_group.this[0].location
}

resource "azurerm_user_assigned_identity" "runtime" {
  name                = "${local.name}-mi"
  location            = local.resource_group_location
  resource_group_name = local.resource_group_name
  tags                = local.common_tags
}

module "servicebus" {
  source = "./modules/servicebus"

  name_prefix         = local.name
  location            = local.resource_group_location
  resource_group_name = local.resource_group_name
  queue_name          = var.servicebus_queue_name
  safe_dlq_queue_name = var.servicebus_deadletter_queue_name
  message_ttl         = var.password_message_ttl
  tags                = local.common_tags
}

module "keyvault" {
  source = "./modules/keyvault"

  name_prefix                 = local.name
  location                    = local.resource_group_location
  resource_group_name         = local.resource_group_name
  tenant_id                   = var.tenant_id
  managed_identity_principal_id = azurerm_user_assigned_identity.runtime.principal_id
  operator_object_ids         = var.key_vault_operator_object_ids
  secret_names                = var.key_vault_secret_names
  tags                        = local.common_tags
}

module "aca" {
  source = "./modules/aca"

  name_prefix                 = local.name
  location                    = local.resource_group_location
  resource_group_name         = local.resource_group_name
  image_name                  = var.image_name
  image_tag                   = var.image_tag
  container_cpu               = var.container_cpu
  container_memory            = var.container_memory
  min_replicas                = var.min_replicas
  max_replicas                = var.max_replicas
  managed_identity_id         = azurerm_user_assigned_identity.runtime.id
  managed_identity_client_id  = azurerm_user_assigned_identity.runtime.client_id
  managed_identity_principal_id = azurerm_user_assigned_identity.runtime.principal_id
  servicebus_namespace_name   = module.servicebus.namespace_name
  servicebus_queue_name       = module.servicebus.queue_name
  servicebus_safe_dlq_queue_name = module.servicebus.safe_dlq_queue_name
  key_vault_url               = module.keyvault.vault_uri
  key_vault_secret_names      = var.key_vault_secret_names
  app_config                  = var.app_config
  tags                        = local.common_tags
}
```

- [ ] **Step 2: Replace root variables placeholder**

Edit `deploy/terraform/variables.tf`:

```hcl
variable "environment" {
  type        = string
  description = "Deployment environment name, such as staging or production."
}

variable "name_prefix" {
  type        = string
  description = "Short resource name prefix."
  default     = "password-hook"
}

variable "location" {
  type        = string
  description = "Azure region."
  default     = "eastasia"
}

variable "create_resource_group" {
  type        = bool
  description = "Whether Terraform should create the resource group."
  default     = true
}

variable "resource_group_name" {
  type        = string
  description = "Resource group name to create or use."
}

variable "tenant_id" {
  type        = string
  description = "Microsoft Entra tenant ID."
}

variable "image_name" {
  type        = string
  description = "Container image repository name."
  default     = "password-hook-service"
}

variable "image_tag" {
  type        = string
  description = "Container image tag to deploy."
}

variable "container_cpu" {
  type        = number
  description = "Container CPU allocation."
  default     = 0.5
}

variable "container_memory" {
  type        = string
  description = "Container memory allocation."
  default     = "1Gi"
}

variable "min_replicas" {
  type        = number
  description = "Minimum Container App replicas."
  default     = 1
}

variable "max_replicas" {
  type        = number
  description = "Maximum Container App replicas."
  default     = 10
}

variable "password_message_ttl" {
  type        = string
  description = "Service Bus password sync message TTL as ISO 8601 duration."
  default     = "PT5M"
}

variable "servicebus_queue_name" {
  type        = string
  description = "Active password sync queue name."
  default     = "password-sync"
}

variable "servicebus_deadletter_queue_name" {
  type        = string
  description = "Application-level safe DLQ queue name."
  default     = "password-sync-dlq"
}

variable "key_vault_operator_object_ids" {
  type        = list(string)
  description = "Operator principal object IDs allowed to set and rotate Key Vault secrets."
  default     = []
}

variable "key_vault_secret_names" {
  type = object({
    hmac_secret                  = string
    servicebus_connection_string = string
    graph_client_secret          = string
    password_encryption_key      = string
  })
  description = "Secret names expected by the application. Values are injected outside Terraform."
  default = {
    hmac_secret                  = "hook-hmac-secret"
    servicebus_connection_string = "servicebus-conn-str"
    graph_client_secret          = "graph-client-secret"
    password_encryption_key      = "password-payload-encryption-key"
  }
}

variable "app_config" {
  type = object({
    entra_primary_domain             = string
    entra_fallback_domain            = string
    problem_base_url                 = string
    graph_tenant_id                  = string
    graph_client_id                  = string
    password_encryption_key_id       = string
    portal_allowed_cidrs             = string
    rate_limit_per_ip                = string
    rate_limit_window                = string
    hook_max_body_bytes              = string
  })
  description = "Non-secret application configuration values."
}

variable "tags" {
  type        = map(string)
  description = "Tags applied to all supported resources."
  default     = {}
}
```

- [ ] **Step 3: Replace root outputs placeholder**

Edit `deploy/terraform/outputs.tf`:

```hcl
output "container_app_url" {
  description = "HTTPS URL for the password hook service."
  value       = module.aca.container_app_url
}

output "acr_login_server" {
  description = "ACR login server."
  value       = module.aca.acr_login_server
}

output "key_vault_uri" {
  description = "Key Vault URI used by the application."
  value       = module.keyvault.vault_uri
}

output "managed_identity_client_id" {
  description = "Container App user-assigned managed identity client ID."
  value       = module.aca.managed_identity_client_id
}

output "servicebus_namespace_name" {
  description = "Service Bus namespace name."
  value       = module.servicebus.namespace_name
}

output "servicebus_queue_name" {
  description = "Active password sync queue name."
  value       = module.servicebus.queue_name
}

output "servicebus_safe_dlq_queue_name" {
  description = "Application-level safe DLQ queue name."
  value       = module.servicebus.safe_dlq_queue_name
}

output "secret_injection_commands" {
  description = "Operator command templates for setting runtime secrets without Terraform state exposure."
  value = {
    hmac_secret = "az keyvault secret set --vault-name ${module.keyvault.vault_name} --name ${var.key_vault_secret_names.hmac_secret} --value '<portal-shared-hmac-secret>'"
    servicebus_connection_string = "az keyvault secret set --vault-name ${module.keyvault.vault_name} --name ${var.key_vault_secret_names.servicebus_connection_string} --value '<operator-created-servicebus-connection-string>'"
    graph_client_secret = "az keyvault secret set --vault-name ${module.keyvault.vault_name} --name ${var.key_vault_secret_names.graph_client_secret} --value '<graph-client-secret>'"
    password_encryption_key = "az keyvault secret set --vault-name ${module.keyvault.vault_name} --name ${var.key_vault_secret_names.password_encryption_key} --value '<base64-32-byte-password-payload-key>'"
  }
}
```

- [ ] **Step 4: Add non-secret staging example**

Create `deploy/terraform/examples/staging.tfvars.example`:

```hcl
environment         = "staging"
resource_group_name = "rg-password-hook-staging"
tenant_id           = "00000000-0000-0000-0000-000000000000"
image_tag           = "staging"

app_config = {
  entra_primary_domain       = "nycu.edu.tw"
  entra_fallback_domain      = "nycu.onmicrosoft.com"
  problem_base_url           = "https://nycu.edu.tw/problems"
  graph_tenant_id            = "00000000-0000-0000-0000-000000000000"
  graph_client_id            = "00000000-0000-0000-0000-000000000000"
  password_encryption_key_id = "password-payload-key-v1"
  portal_allowed_cidrs       = "203.0.113.155/32,203.0.113.177/32"
  rate_limit_per_ip          = "500"
  rate_limit_window          = "1s"
  hook_max_body_bytes        = "65536"
}

tags = {
  owner = "identity"
}
```

- [ ] **Step 5: Run Terraform formatting**

Run:

```bash
terraform -chdir=deploy/terraform fmt -recursive
```

Expected: PASS and formatted files only.

- [ ] **Step 6: Commit**

```bash
git add deploy/terraform/main.tf deploy/terraform/variables.tf deploy/terraform/outputs.tf deploy/terraform/examples/staging.tfvars.example
git commit -m "feat: define infrastructure root module"
```

### Task 2: Service Bus Module

**Files:**
- Modify: `deploy/terraform/modules/servicebus/main.tf`
- Create: `deploy/terraform/modules/servicebus/variables.tf`
- Create: `deploy/terraform/modules/servicebus/outputs.tf`

- [ ] **Step 1: Create Service Bus module variables**

Create `deploy/terraform/modules/servicebus/variables.tf`:

```hcl
variable "name_prefix" {
  type        = string
  description = "Resource name prefix."
}

variable "location" {
  type        = string
  description = "Azure region."
}

variable "resource_group_name" {
  type        = string
  description = "Resource group name."
}

variable "queue_name" {
  type        = string
  description = "Active password sync queue name."
}

variable "safe_dlq_queue_name" {
  type        = string
  description = "Application-level safe DLQ queue name."
}

variable "message_ttl" {
  type        = string
  description = "Default message TTL for password sync jobs."
}

variable "tags" {
  type        = map(string)
  description = "Resource tags."
}
```

- [ ] **Step 2: Replace Service Bus module placeholder**

Edit `deploy/terraform/modules/servicebus/main.tf`:

```hcl
resource "azurerm_servicebus_namespace" "this" {
  name                = "${var.name_prefix}-sb"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "Standard"
  tags                = var.tags
}

resource "azurerm_servicebus_queue" "password_sync" {
  name                                    = var.queue_name
  namespace_id                            = azurerm_servicebus_namespace.this.id
  default_message_ttl                     = var.message_ttl
  dead_lettering_on_message_expiration    = false
  max_delivery_count                      = 10
  requires_duplicate_detection            = false
}

resource "azurerm_servicebus_queue" "safe_dlq" {
  name                                 = var.safe_dlq_queue_name
  namespace_id                         = azurerm_servicebus_namespace.this.id
  default_message_ttl                  = "P14D"
  dead_lettering_on_message_expiration = false
  max_delivery_count                   = 10
}
```

Promotion note: do not add `azurerm_servicebus_namespace_authorization_rule` or `azurerm_servicebus_queue_authorization_rule` unless the owner accepts that generated keys enter Terraform state. If the active app still needs one connection string that can send to the active queue, listen to the active queue, and send to the safe DLQ, operators should create a namespace-level Send/Listen SAS credential outside Terraform and store it in Key Vault. Prefer app support for Managed Identity or split credentials before implementation if the owner wants narrower runtime access.

- [ ] **Step 3: Create Service Bus module outputs**

Create `deploy/terraform/modules/servicebus/outputs.tf`:

```hcl
output "namespace_id" {
  value = azurerm_servicebus_namespace.this.id
}

output "namespace_name" {
  value = azurerm_servicebus_namespace.this.name
}

output "queue_id" {
  value = azurerm_servicebus_queue.password_sync.id
}

output "queue_name" {
  value = azurerm_servicebus_queue.password_sync.name
}

output "safe_dlq_queue_id" {
  value = azurerm_servicebus_queue.safe_dlq.id
}

output "safe_dlq_queue_name" {
  value = azurerm_servicebus_queue.safe_dlq.name
}

```

- [ ] **Step 4: Run Terraform formatting**

Run:

```bash
terraform -chdir=deploy/terraform fmt -recursive
```

Expected: PASS. Full root validation waits until all referenced modules exist.

- [ ] **Step 5: Commit**

```bash
git add deploy/terraform/modules/servicebus/main.tf deploy/terraform/modules/servicebus/variables.tf deploy/terraform/modules/servicebus/outputs.tf
git commit -m "feat: add service bus infrastructure module"
```

### Task 3: Key Vault Module Without Secret Values In State

**Files:**
- Modify: `deploy/terraform/modules/keyvault/main.tf`
- Create: `deploy/terraform/modules/keyvault/variables.tf`
- Create: `deploy/terraform/modules/keyvault/outputs.tf`

- [ ] **Step 1: Create Key Vault module variables**

Create `deploy/terraform/modules/keyvault/variables.tf`:

```hcl
variable "name_prefix" {
  type        = string
  description = "Resource name prefix."
}

variable "location" {
  type        = string
  description = "Azure region."
}

variable "resource_group_name" {
  type        = string
  description = "Resource group name."
}

variable "tenant_id" {
  type        = string
  description = "Microsoft Entra tenant ID."
}

variable "managed_identity_principal_id" {
  type        = string
  description = "Container App managed identity principal ID."
}

variable "operator_object_ids" {
  type        = list(string)
  description = "Operator object IDs allowed to manage runtime secrets."
}

variable "secret_names" {
  type = object({
    hmac_secret                  = string
    servicebus_connection_string = string
    graph_client_secret          = string
    password_encryption_key      = string
  })
  description = "Secret names expected by the application."
}

variable "tags" {
  type        = map(string)
  description = "Resource tags."
}
```

- [ ] **Step 2: Replace Key Vault module placeholder**

Edit `deploy/terraform/modules/keyvault/main.tf`:

```hcl
resource "azurerm_key_vault" "this" {
  name                       = replace("${var.name_prefix}-kv", "-", "")
  location                   = var.location
  resource_group_name        = var.resource_group_name
  tenant_id                  = var.tenant_id
  sku_name                   = "standard"
  soft_delete_retention_days = 90
  purge_protection_enabled   = true
  tags                       = var.tags
}

resource "azurerm_key_vault_access_policy" "runtime" {
  key_vault_id = azurerm_key_vault.this.id
  tenant_id    = var.tenant_id
  object_id    = var.managed_identity_principal_id

  secret_permissions = [
    "Get",
  ]
}

resource "azurerm_key_vault_access_policy" "operators" {
  for_each = toset(var.operator_object_ids)

  key_vault_id = azurerm_key_vault.this.id
  tenant_id    = var.tenant_id
  object_id    = each.value

  secret_permissions = [
    "Get",
    "List",
    "Set",
    "Delete",
    "Recover",
  ]
}
```

Do not add `azurerm_key_vault_secret` resources for runtime secrets in this task. Secret values would be stored in Terraform state.

- [ ] **Step 3: Create Key Vault module outputs**

Create `deploy/terraform/modules/keyvault/outputs.tf`:

```hcl
output "vault_id" {
  value = azurerm_key_vault.this.id
}

output "vault_name" {
  value = azurerm_key_vault.this.name
}

output "vault_uri" {
  value = azurerm_key_vault.this.vault_uri
}

output "secret_names" {
  value = var.secret_names
}
```

- [ ] **Step 4: Run Terraform formatting**

Run:

```bash
terraform -chdir=deploy/terraform fmt -recursive
```

Expected: PASS. Full root validation waits until all referenced modules exist.

- [ ] **Step 5: Commit**

```bash
git add deploy/terraform/modules/keyvault/main.tf deploy/terraform/modules/keyvault/variables.tf deploy/terraform/modules/keyvault/outputs.tf
git commit -m "feat: add key vault infrastructure module"
```

### Task 4: Container Apps And ACR Module

**Files:**
- Modify: `deploy/terraform/modules/aca/main.tf`
- Create: `deploy/terraform/modules/aca/variables.tf`
- Create: `deploy/terraform/modules/aca/outputs.tf`

- [ ] **Step 1: Create ACA module variables**

Create `deploy/terraform/modules/aca/variables.tf`:

```hcl
variable "name_prefix" {
  type        = string
  description = "Resource name prefix."
}

variable "location" {
  type        = string
  description = "Azure region."
}

variable "resource_group_name" {
  type        = string
  description = "Resource group name."
}

variable "image_name" {
  type        = string
  description = "Image repository name."
}

variable "image_tag" {
  type        = string
  description = "Image tag."
}

variable "container_cpu" {
  type        = number
  description = "Container CPU allocation."
}

variable "container_memory" {
  type        = string
  description = "Container memory allocation."
}

variable "min_replicas" {
  type        = number
  description = "Minimum replicas."
}

variable "max_replicas" {
  type        = number
  description = "Maximum replicas."
}

variable "managed_identity_id" {
  type        = string
  description = "User-assigned managed identity resource ID."
}

variable "managed_identity_client_id" {
  type        = string
  description = "User-assigned managed identity client ID."
}

variable "managed_identity_principal_id" {
  type        = string
  description = "User-assigned managed identity principal ID."
}

variable "servicebus_namespace_name" {
  type        = string
  description = "Service Bus namespace name."
}

variable "servicebus_queue_name" {
  type        = string
  description = "Active queue name used for scale rules."
}

variable "servicebus_safe_dlq_queue_name" {
  type        = string
  description = "Application-level safe DLQ queue name."
}

variable "key_vault_url" {
  type        = string
  description = "Key Vault URL."
}

variable "key_vault_secret_names" {
  type = object({
    hmac_secret                  = string
    servicebus_connection_string = string
    graph_client_secret          = string
    password_encryption_key      = string
  })
  description = "Key Vault secret names expected by the application."
}

variable "app_config" {
  type = object({
    entra_primary_domain             = string
    entra_fallback_domain            = string
    problem_base_url                 = string
    graph_tenant_id                  = string
    graph_client_id                  = string
    password_encryption_key_id       = string
    portal_allowed_cidrs             = string
    rate_limit_per_ip                = string
    rate_limit_window                = string
    hook_max_body_bytes              = string
  })
  description = "Non-secret application configuration."
}

variable "tags" {
  type        = map(string)
  description = "Resource tags."
}
```

- [ ] **Step 2: Replace ACA module placeholder**

Edit `deploy/terraform/modules/aca/main.tf`:

```hcl
resource "azurerm_log_analytics_workspace" "this" {
  name                = "${var.name_prefix}-law"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "PerGB2018"
  retention_in_days   = 30
  tags                = var.tags
}

resource "azurerm_container_registry" "this" {
  name                = replace("${var.name_prefix}acr", "-", "")
  resource_group_name = var.resource_group_name
  location            = var.location
  sku                 = "Basic"
  admin_enabled       = false
  tags                = var.tags
}

resource "azurerm_role_assignment" "acr_pull" {
  scope                = azurerm_container_registry.this.id
  role_definition_name = "AcrPull"
  principal_id         = var.managed_identity_principal_id
}

resource "azurerm_container_app_environment" "this" {
  name                       = "${var.name_prefix}-cae"
  location                   = var.location
  resource_group_name        = var.resource_group_name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.this.id
  tags                       = var.tags
}

resource "azurerm_container_app" "this" {
  name                         = "${var.name_prefix}-app"
  container_app_environment_id = azurerm_container_app_environment.this.id
  resource_group_name          = var.resource_group_name
  revision_mode                = "Single"
  tags                         = var.tags

  identity {
    type         = "UserAssigned"
    identity_ids = [var.managed_identity_id]
  }

  registry {
    server   = azurerm_container_registry.this.login_server
    identity = var.managed_identity_id
  }

  ingress {
    external_enabled = true
    target_port      = 8080
    transport        = "auto"

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  template {
    min_replicas = var.min_replicas
    max_replicas = var.max_replicas

    container {
      name   = "password-hook-service"
      image  = "${azurerm_container_registry.this.login_server}/${var.image_name}:${var.image_tag}"
      cpu    = var.container_cpu
      memory = var.container_memory

      env {
        name  = "HTTP_ADDR"
        value = ":8080"
      }

      env {
        name  = "SECRETS_SOURCE"
        value = "keyvault"
      }

      env {
        name  = "KEY_VAULT_URL"
        value = var.key_vault_url
      }

      env {
        name  = "KEY_VAULT_HMAC_SECRET_NAME"
        value = var.key_vault_secret_names.hmac_secret
      }

      env {
        name  = "KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME"
        value = var.key_vault_secret_names.servicebus_connection_string
      }

      env {
        name  = "KEY_VAULT_GRAPH_CLIENT_SECRET_NAME"
        value = var.key_vault_secret_names.graph_client_secret
      }

      env {
        name  = "KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME"
        value = var.key_vault_secret_names.password_encryption_key
      }

      env {
        name  = "ENTRA_PRIMARY_DOMAIN"
        value = var.app_config.entra_primary_domain
      }

      env {
        name  = "ENTRA_FALLBACK_DOMAIN"
        value = var.app_config.entra_fallback_domain
      }

      env {
        name  = "PROBLEM_BASE_URL"
        value = var.app_config.problem_base_url
      }

      env {
        name  = "SERVICEBUS_QUEUE_NAME"
        value = var.servicebus_queue_name
      }

      env {
        name  = "SERVICEBUS_DEADLETTER_QUEUE_NAME"
        value = var.servicebus_safe_dlq_queue_name
      }

      env {
        name  = "PASSWORD_ENCRYPTION_KEY_ID"
        value = var.app_config.password_encryption_key_id
      }

      env {
        name  = "GRAPH_TENANT_ID"
        value = var.app_config.graph_tenant_id
      }

      env {
        name  = "GRAPH_CLIENT_ID"
        value = var.app_config.graph_client_id
      }

      env {
        name  = "PORTAL_ALLOWED_CIDRS"
        value = var.app_config.portal_allowed_cidrs
      }

      env {
        name  = "RATE_LIMIT_PER_IP"
        value = var.app_config.rate_limit_per_ip
      }

      env {
        name  = "RATE_LIMIT_WINDOW"
        value = var.app_config.rate_limit_window
      }

      env {
        name  = "HOOK_MAX_BODY_BYTES"
        value = var.app_config.hook_max_body_bytes
      }
    }

    custom_scale_rule {
      name             = "servicebus-queue-depth"
      custom_rule_type = "azure-servicebus"
      metadata = {
        namespace = var.servicebus_namespace_name
        queueName = var.servicebus_queue_name
        messageCount = "50"
      }
    }
  }
}
```

Promotion notes:
- Verify current provider syntax for `custom_scale_rule`, ACR identity auth, and Service Bus scaler authentication. If the scaler requires a secret connection string, add the minimum required secret reference and keep its value injected outside Terraform.

- [ ] **Step 3: Create ACA module outputs**

Create `deploy/terraform/modules/aca/outputs.tf`:

```hcl
output "container_app_id" {
  value = azurerm_container_app.this.id
}

output "container_app_url" {
  value = "https://${azurerm_container_app.this.latest_revision_fqdn}"
}

output "acr_login_server" {
  value = azurerm_container_registry.this.login_server
}

output "managed_identity_id" {
  value = var.managed_identity_id
}

output "managed_identity_client_id" {
  value = var.managed_identity_client_id
}

output "managed_identity_principal_id" {
  value = var.managed_identity_principal_id
}

output "container_app_environment_id" {
  value = azurerm_container_app_environment.this.id
}
```

- [ ] **Step 4: Run Terraform formatting and validation**

Run:

```bash
terraform -chdir=deploy/terraform fmt -recursive
terraform -chdir=deploy/terraform validate
```

Expected: PASS after resolving provider syntax discovered during promotion refresh.

- [ ] **Step 5: Commit**

```bash
git add deploy/terraform/modules/aca/main.tf deploy/terraform/modules/aca/variables.tf deploy/terraform/modules/aca/outputs.tf
git commit -m "feat: add container apps infrastructure module"
```

### Task 5: Secret Injection And Infrastructure Documentation

**Files:**
- Create: `deploy/terraform/README.md`
- Modify: `README.md`

- [ ] **Step 1: Create Terraform README**

Create `deploy/terraform/README.md`:

```markdown
# Password Hook Service Terraform

This Terraform stack provisions the Azure resources needed to deploy the password hook service:

- Azure Container Registry
- Log Analytics workspace
- Azure Container Apps environment and app
- User-assigned managed identity
- Azure Service Bus namespace, password sync queue, and safe DLQ queue
- Azure Key Vault and secret read grants for the app identity

## State Safety

Do not put runtime secret values in Terraform variables, `.tfvars`, `azurerm_key_vault_secret`, outputs, or logs. Terraform state is not the secret store for this service.

Terraform creates the Key Vault and publishes command templates for operators. Operators set these values directly in Key Vault:

- `hook-hmac-secret`
- `servicebus-conn-str`
- `graph-client-secret`
- `password-payload-encryption-key`

If the application still uses one Service Bus connection string, create that SAS credential outside Terraform so generated keys do not enter Terraform state. Until the app supports Managed Identity or split queue credentials, the credential must be able to send to the active queue, listen to the active queue, and send to the safe DLQ queue.

## Local Workflow

```bash
terraform -chdir=deploy/terraform fmt -recursive
terraform -chdir=deploy/terraform init
terraform -chdir=deploy/terraform validate
terraform -chdir=deploy/terraform plan -var-file=examples/staging.tfvars
```

## Secret Injection

After `terraform apply`, read the `secret_injection_commands` output and replace placeholder values with approved secret values.

Example:

```bash
terraform -chdir=deploy/terraform output secret_injection_commands
az keyvault secret set --vault-name <vault-name> --name hook-hmac-secret --value '<portal-shared-hmac-secret>'
az keyvault secret set --vault-name <vault-name> --name servicebus-conn-str --value '<operator-created-servicebus-connection-string>'
az keyvault secret set --vault-name <vault-name> --name graph-client-secret --value '<graph-client-secret>'
az keyvault secret set --vault-name <vault-name> --name password-payload-encryption-key --value '<base64-32-byte-password-payload-key>'
```

## Deployment Notes

The Container App uses `SECRETS_SOURCE=keyvault`. The managed identity assigned to the app must have `Get` permission for Key Vault secrets.

The first deployment needs an image bootstrap path. Create ACR first, push the `password-hook-service` image tag, then create or update the Container App to point at that existing tag. Do not apply a Container App revision that references an image tag that has not been pushed.

The active password sync queue defaults to `password-sync`. The application-level safe DLQ queue defaults to `password-sync-dlq`. Native Service Bus DLQ is not the password sync terminal-failure path.

The Container App scales from Service Bus queue depth with a target of 50 active messages per replica.
```

- [ ] **Step 2: Link Terraform README from root README**

Add to the root `README.md` after the Docker run section:

```markdown
## Azure Infrastructure

Terraform deployment planning lives in `deploy/terraform/`. The infrastructure stack provisions Azure Container Apps, Azure Container Registry, Service Bus, Key Vault, managed identity, and queue-depth scaling resources.

Runtime secret values are injected into Key Vault outside Terraform so they do not enter Terraform state.
```

- [ ] **Step 3: Run docs sanity checks**

Run:

```bash
rg -n "azurerm_key_vault_secret|<portal-shared-hmac-secret>|servicebus-conn-str|password-payload-encryption-key|Terraform state" deploy/terraform README.md
```

Expected:
- `azurerm_key_vault_secret` appears only in warnings or prohibitions.
- Placeholder secret values appear only in command examples.
- Terraform state safety is documented.

- [ ] **Step 4: Commit**

```bash
git add deploy/terraform/README.md README.md
git commit -m "docs: document infrastructure deployment workflow"
```

### Task 6: Terraform Validation And Scope Check

**Files:**
- Verify: `deploy/terraform/**`
- Verify: `README.md`

- [ ] **Step 1: Run Terraform formatting**

Run:

```bash
terraform -chdir=deploy/terraform fmt -recursive -check
```

Expected: PASS.

- [ ] **Step 2: Run Terraform initialization and validation**

Run:

```bash
terraform -chdir=deploy/terraform init -backend=false
terraform -chdir=deploy/terraform validate
```

Expected: PASS. If provider download requires network access, run in the approved environment and record that `init` fetched the AzureRM provider.

- [ ] **Step 3: Run secret-state safety scan**

Run:

```bash
rg -n "azurerm_key_vault_secret|HOOK_HMAC_SECRET\\s*=|GRAPH_CLIENT_SECRET\\s*=|PASSWORD_ENCRYPTION_KEY_B64\\s*=|SharedAccessKey=" deploy/terraform --glob '!deploy/terraform/README.md'
```

Expected: no matches. The Terraform code should not contain direct secret values or Key Vault secret value resources.

- [ ] **Step 4: Run app verification to catch README/config drift**

Run:

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go vet ./...
```

Expected: PASS. Infrastructure changes should not require production Go code changes.

- [ ] **Step 5: Verify slice scope**

Run:

```bash
git diff --name-only HEAD~5..HEAD
```

Expected changed paths are limited to:

```text
README.md
deploy/terraform/README.md
deploy/terraform/examples/staging.tfvars.example
deploy/terraform/main.tf
deploy/terraform/outputs.tf
deploy/terraform/variables.tf
deploy/terraform/modules/aca/main.tf
deploy/terraform/modules/aca/outputs.tf
deploy/terraform/modules/aca/variables.tf
deploy/terraform/modules/keyvault/main.tf
deploy/terraform/modules/keyvault/outputs.tf
deploy/terraform/modules/keyvault/variables.tf
deploy/terraform/modules/servicebus/main.tf
deploy/terraform/modules/servicebus/outputs.tf
deploy/terraform/modules/servicebus/variables.tf
```

No files under `internal/`, `cmd/`, or `pkg/` should change in this slice unless the promoted plan is explicitly revised.

---

## Self-Review

- Spec coverage: This draft covers Slice 10 roadmap done criteria: ACA, Service Bus, Key Vault, ACR, managed identities, scaling rules, Terraform module structure, and deployment documentation.
- Boundary check: This draft excludes CI/CD, scanner gates, dashboards, alerts, WAF, Front Door, private networking, DDoS, DNS cutover, and production runbooks.
- Secret-state check: The default plan avoids managing runtime secret values through Terraform resources or variables.
- Placeholder scan: No unresolved implementation placeholders are intended; promotion must refresh AzureRM provider syntax before execution.
- Type consistency: New variables and outputs are consistently named across root and modules: `servicebus_queue_name`, `servicebus_deadletter_queue_name`, `key_vault_secret_names`, `app_config`, `managed_identity_client_id`, and `container_app_url`.
