# Slice 10 Infrastructure and Durable Sync-Status Implementation Plan

> **Plan Status:** Active
>
> **Source Refresh:** Refreshed on 2026-07-12 against the completed Slice 8A Azure Monitor exporter, Slice 9 API protection, and Slice 10A Service Bus managed identity work. The owner explicitly approved durable, shared sync-status storage as part of Slice 10.
>
> **For agentic workers:** Execute one task at a time. Do not start a later task until the preceding task's checks pass. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provision a deployable private Azure implementation of password-hook-service that on-premises portal web servers call over an approved site-to-site VPN through an existing Azure Application Gateway WAF_v2 private frontend, and replace the per-replica in-memory sync-status store with an Azure Managed Redis-backed store, so `login_bootstrap` deduplication and out-of-order protections remain correct across replicas and restarts.

**Architecture:** Terraform creates or consumes the approved hybrid-network resources (VNet, route-based site-to-site VPN, private DNS resolution, and ACA infrastructure subnet), then deploys an internal Azure Container Apps environment, Service Bus, Key Vault, Application Insights/Log Analytics, Azure Managed Redis, the approved existing ACR, and a single user-assigned runtime identity. It adds a dedicated static private frontend, HTTPS listener, WAF policy association, backend setting, probe, and routing rule to the approved existing Application Gateway WAF_v2 without changing its existing public frontends/listeners/rules. On-premises portal web servers resolve a private HTTPS hostname to that Application Gateway private frontend and reach it over the VPN; the gateway re-encrypts traffic to internal ACA. The application uses the runtime identity for Key Vault, Service Bus, Azure Monitor custom metrics, Azure Managed Redis, and ACR image pulls; it stores no runtime connection strings. The Redis store preserves the existing `syncstatus.Store` semantics, including the `SourceEnqueuedAt` ordering guard. Runtime secrets remain operator-injected into Key Vault and never enter Terraform inputs, state, or outputs.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, `github.com/redis/go-redis-entraid`, Azure Managed Redis with Microsoft Entra authentication and TLS, Terraform `>= 1.8.0`, AzureRM 4.x, AzAPI, Azure Container Apps, Azure Service Bus Standard, Azure Key Vault RBAC, Azure Monitor/Application Insights.

## Active Constraints

- Production must use `SERVICEBUS_AUTH_MODE=managed_identity`; do not create Service Bus SAS rules or inject a Service Bus connection string for production.
- Azure Managed Redis must use Entra authentication and TLS. Disable access-key authentication after the runtime identity's data access is provisioned; do not put Redis access keys in Key Vault, app config, Terraform state, or docs.
- Preserve the existing sync-status state-machine semantics: unknown UPNs are unsynced; `sync_pending` suppresses only fresh `login_bootstrap` events; `synced` suppresses `login_bootstrap`; `sync_failed` allows a later bootstrap; stale outcome writes are ignored by `SourceEnqueuedAt`.
- The shared-store outage posture remains fail-open for hook deduplication and best-effort for worker status recording, matching existing `migration.Service` and `worker.Worker` behavior. It must never cause password plaintext, ciphertext, queue bodies, tokens, or secret values to be logged.
- Use Azure Key Vault RBAC (`enable_rbac_authorization = true`), not legacy access policies. Runtime needs `Key Vault Secrets User`; named operators need `Key Vault Secrets Officer` only where secret injection/rotation is intended.
- Include the completed Slice 8A telemetry requirements: Application Insights, an ACA managed OpenTelemetry agent configured through AzAPI, and `Monitoring Metrics Publisher` on the chosen custom-metrics resource. Do not set `OTEL_EXPORTER_OTLP_ENDPOINT` in the app spec because ACA injects it.
- The hook API must be internal-only: on-premises portal web servers reach a private Application Gateway WAF_v2 frontend over an approved route-based site-to-site VPN using a private DNS name and HTTPS; the gateway reaches ACA internal ingress. Do not expose the hook through public ACA ingress, Front Door, or an internet-facing reverse proxy in this slice.
- The plan must either create the required VPN/DNS resources or consume approved existing hub-network resources; it must never silently assume either model. Public/private endpoint decisions for ACR, Key Vault, Service Bus, and Managed Redis must be explicit and validated against the selected network topology.
- Reuse the approved existing staging/production Application Gateway WAF_v2 resources. Add only a static private frontend and dedicated password-hook listener/rule path; do not remove, replace, stop, resize, or alter existing public frontend/listener/rule paths. Use a staging-first change window and rollback procedure for every gateway update.
- Do not implement Front Door, alert rules, dashboards, CI/CD, scanner gates, or production runbooks in this slice. Document and verify the Application Gateway-to-ACA and portal-to-Application-Gateway source-address behavior required before rollout.
- Globally constrained resource names must use validated normalized prefixes plus a deterministic suffix. Validate limits before `terraform apply`.

## Delivery Shape

1. Confirm the private-network contract for the on-premises portal web servers before creating application infrastructure.
2. Add a Redis-backed `syncstatus.Store` plus configuration, lifecycle wiring, and unit tests.
3. Build Terraform modules for Service Bus, Key Vault, Azure Managed Redis, and a private ACA/observability deployment, using the completed managed-identity model.
4. Use the approved existing ACR before the first Container App revision, inject only HMAC/Graph/password-encryption values into Key Vault, then deploy and validate the app image from an on-premises web server over the site-to-site VPN.

### Task 0: Private Network Contract and On-Premises Caller Prerequisites

**Files:**
- Modify: `deploy/terraform/variables.tf`, `deploy/terraform/examples/staging.tfvars.example`
- Create: `deploy/terraform/modules/network/main.tf`, `deploy/terraform/modules/network/variables.tf`, `deploy/terraform/modules/network/outputs.tf`
- Create: `deploy/terraform/modules/applicationgateway/main.tf`, `deploy/terraform/modules/applicationgateway/variables.tf`, `deploy/terraform/modules/applicationgateway/outputs.tf`
- Create: `docs/superpowers/plans/active/2026-07-03-slice-10-private-network-decisions.md`

- [ ] Record and obtain owner approval for the deployment boundary before creating Terraform: Azure subscription/resource group, target region, VNet and address-space ownership, non-overlapping ACA infrastructure subnet, dedicated `GatewaySubnet`, on-premises portal-web-server CIDRs, on-premises VPN device public IP and BGP/static-route mode, and the identity authorised to change Azure network resources and on-premises DNS/VPN configuration. Stop if address spaces overlap or any route owner is unknown.
- [ ] Before choosing Terraform inputs, use the current Azure CLI login only for scoped read-only discovery. Record the selected subscription/tenant and inventory the approved existing ACR, resource groups, VNet/subnets, VPN Gateway, Local Network Gateway, VPN connections, route tables, private DNS zones/resolver endpoints, and relevant Key Vault, Service Bus, and Managed Redis resources. Prefer `az account show`, `az resource list --resource-group`, service-specific `show` commands, or a subscription-scoped `az graph query`; require Reader-equivalent access and do not run `create`, `update`, `delete`, `apply`, role-assignment, secret-read, or credential-list commands.
- [ ] Inventory the approved staging and production Application Gateway WAF_v2 resources with read-only CLI: SKU/tier/provisioning state, dedicated subnet/address capacity, existing public/private frontend configurations, listeners, routing rules/priorities, backend pools/settings/probes, WAF policy/mode/custom rules, TLS certificate references, and the resource's Terraform/portal ownership. The currently observed gateways are `agw-stg-jpe-001` and `agw-prod-jpe-001` in `rg-spoke-paas`, each WAF_v2 with a healthy public-only frontend; re-check before any write. Do not import, recreate, or manage the complete existing gateway from this repository unless its current state owner explicitly transfers ownership.
- [ ] Decide whether Terraform creates the VNet, VPN Gateway, Local Network Gateway, and site-to-site VPN connection, or consumes approved existing network resource IDs. Do not create a parallel VPN connection, GatewaySubnet, or DNS resolver when a centrally managed hub already supplies them. Model the chosen ownership explicitly with mutually exclusive validated inputs.
- [ ] Require a route-based VPN Gateway and an established site-to-site connection between the on-premises network and the Application Gateway VNet (or its peered hub). Add only the routes required for portal web servers to reach the selected Application Gateway static private frontend IP on TCP 443; do not advertise or permit a portal-web-server route directly to the ACA environment ingress IP, and do not introduce default-route/forced-tunnel changes in this slice.
- [ ] Define the private API DNS contract before creating ACA: use an approved hostname such as `password-hook.<internal-domain>` or the approved `*.nycu.edu.tw` name; configure split-horizon/on-premises DNS so that portal web servers receive the Application Gateway static private frontend IP. Do not publish an RFC1918 A record in public authoritative DNS. Azure Private Resolver conditional forwarding for the ACA default domain is not a portal-call prerequisite in this design; retain it only for separately approved hybrid private-zone needs.
- [ ] Reserve a static unused IP in the Application Gateway dedicated subnet and verify the subnet has sufficient capacity for WAF_v2 scaling plus one private frontend. Create a new private HTTPS listener and routing rule only after its IP is tied to the rule, because an unbound private frontend IP is not reserved against future gateway scale-out. Preserve all existing public frontend configurations, listeners, rules, certificate bindings, and priorities.
- [ ] Define the caller deployment environment: each on-premises portal web server needs the resolved private API hostname, a trust chain for the Application Gateway listener certificate, outbound TCP 443 through the VPN, the production HMAC secret from the approved on-premises secret store, and an application setting containing the private HTTPS endpoint. Do not put the HMAC secret in Terraform, tfvars, shell history, source control, or this plan.
- [ ] Add a pre-deployment walkthrough checklist: from each portal web server, resolve the private hostname; verify its answer is the approved Application Gateway private frontend IP; establish TLS with the expected SNI/hostname; call `GET /healthz`; send one signed staging hook request; confirm `202 Accepted`; verify WAF and application logs show the expected allowed source path; and confirm no request body, password, HMAC value, signature, nonce, or token appears in output or logs.
- [ ] Record private-network prerequisites that remain outside this slice only if a named platform team owns them and supplies completed resource IDs, route tables, DNS forwarders, and change windows. The application deployment cannot claim readiness merely because Terraform validates.
- [ ] Commit: `docs(terraform): define private ACA and S2S VPN prerequisites`.

### Task 1: Redis-Backed Shared Sync Status

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/syncstatus/status.go`, `internal/syncstatus/status_test.go`
- Create: `internal/syncstatus/redis.go`, `internal/syncstatus/redis_test.go`
- Modify: `internal/app/app.go`, `internal/app/app_test.go`
- Modify: `README.md`

- [ ] Add non-secret Redis configuration: `SYNC_STATUS_STORE=redis`, `REDIS_HOST`, `REDIS_PORT`, `REDIS_KEY_PREFIX`, and `SYNC_STATUS_TERMINAL_TTL`; make all connection/retention settings required or safely defaulted in Redis mode, default the key prefix to a deployment-neutral `password-hook:sync-status:`, and reject an unknown store mode. Add a configurable `PASSWORD_MESSAGE_TTL` Go duration (default `5m`) so the application pending TTL stays aligned with the Terraform Service Bus TTL. Retain the in-memory store only as the explicit local/test mode.
- [ ] Add `go-redis/v9` and `go-redis-entraid`. Construct the production client with `entraid.NewDefaultAzureIdentityProvider`, TLS 1.2+, and the `<host>:<tls-port>` address. Do not add a password, access key, connection string, or an environment secret for Redis.
- [ ] Implement `RedisStore` behind the existing `syncstatus.Store` interface. Store only status plus UTC `UpdatedAt` and `SourceEnqueuedAt`; keys must be a stable one-way digest of the normalized UPN beneath `REDIS_KEY_PREFIX`, never a raw UPN. `Get` returns the existing zero-value record for a missing key.
- [ ] Use one Lua script for every `MarkPending`, `MarkSynced`, and `MarkFailed`: compare the candidate `SourceEnqueuedAt` with the stored value, do nothing for an older candidate, otherwise atomically set the new record. Give pending records a TTL equal to `PasswordMessageTTL`; give terminal states a separately configured retention TTL, chosen during implementation and documented in `README.md`. The script and tests must cover equal timestamps deliberately, so retry delivery has deterministic behavior.
- [ ] Make Redis connection setup fail fast with a bounded `PING` during `app.New`, retain a closer for the client, and pass that same store instance to both `migration.ServiceOptions.SyncStatusStore` and `worker.Options.SyncStatusRecorder`. Keep `NewWithQueue` test-oriented and memory-backed unless a test explicitly injects a store.
- [ ] Add `github.com/alicebob/miniredis/v2` as a test-only dependency and write unit/in-process integration tests for config validation, absent keys, record round trip, pending expiration, stale-write rejection, equal-timestamp behavior, key privacy, context cancellation, and Redis/client error propagation. Extend app tests to prove the production assembly selects the Redis store and closes it on wiring failure/shutdown without requiring a live Azure service.
- [ ] Run `go mod tidy`, `go test ./internal/config ./internal/syncstatus ./internal/migration ./internal/worker ./internal/app`, `go test ./...`, and `go vet ./...`.
- [ ] Commit: `feat(syncstatus): add Redis-backed shared sync status`.

### Task 2: Terraform Root, Naming, and Deployment Inputs

**Files:**
- Modify: `deploy/terraform/main.tf`, `deploy/terraform/variables.tf`, `deploy/terraform/outputs.tf`
- Create: `deploy/terraform/examples/staging.tfvars.example`

- [ ] Replace the placeholder root with required providers for AzureRM, AzAPI, and Random. Pin the AzureRM 4.x and AzAPI versions selected during `terraform init`; commit `.terraform.lock.hcl` once initialization is complete.
- [ ] Add a `random_string` suffix with lowercase alphanumeric output. Derive distinct normalized names for Key Vault, Service Bus, Managed Redis, Log Analytics, Application Insights, ACA environment, and Container App; use the approved existing ACR resource ID/name rather than derive or create an ACR name. Add Terraform validation for `environment`, resource-group name, queue names, image tag, location, internal API hostname, on-premises CIDRs, network-mode inputs, and all resource-name length/character limits before any provider call.
- [ ] Support both a Terraform-created and existing resource group. Create one user-assigned runtime identity in the root and pass its resource ID, client ID, and principal ID to every module needing it.
- [ ] Define typed, non-secret inputs for app image, existing ACR resource ID, an explicit `deploy_container_app` bootstrap gate, replica limits, one numeric password-message TTL that renders as both the Service Bus ISO-8601 duration and `PASSWORD_MESSAGE_TTL` Go duration, Redis SKU and terminal-state retention, Application Insights retention, portal allowlist/rate settings, Graph identifiers, password encryption key ID, private API hostname/listener-certificate reference, existing Application Gateway resource ID, dedicated password-hook WAF policy configuration, dedicated-subnet static private frontend IP, listener/rule priorities, backend hostname/probe path, and the selected existing-or-created network resource IDs. Remove the obsolete Service Bus connection-string secret name from the production object.
- [ ] Wire `network`, `applicationgateway`, `servicebus`, `keyvault`, `redis`, and `aca` modules with explicit dependencies where VPN/DNS routing, Application Gateway backend readiness, private endpoint/DNS records, role-assignment propagation, or telemetry configuration requires them. Pass the Redis hostname/TLS port and selected key prefix to ACA as non-secret configuration.
- [ ] Output only resource identifiers and operator-safe values: private API hostname, Application Gateway private frontend IP/listener/rule IDs, ACA environment static ingress IP/ID, existing ACR login server/resource ID, Key Vault URI, Service Bus namespace FQDN and queue names, Managed Redis hostname/TLS port, identity IDs, Application Insights/metrics resource IDs, and DNS/VPN resource IDs. Do not output secret-value command templates.
- [ ] Add an example tfvars file containing placeholders/non-secret values only. It must reference a placeholder existing ACR resource ID and network resource IDs, use documentation-only private CIDRs, and never include real tenant IDs, client IDs, on-premises public IPs, shared keys, or production resource names.
- [ ] Run `terraform -chdir=deploy/terraform fmt -recursive`; root `validate` waits until all module tasks are complete.
- [ ] Commit: `feat(terraform): define infrastructure root and deployment inputs`.

### Task 3: Service Bus Module and Least-Privilege Runtime RBAC

**Files:**
- Modify: `deploy/terraform/modules/servicebus/main.tf`
- Create: `deploy/terraform/modules/servicebus/variables.tf`, `deploy/terraform/modules/servicebus/outputs.tf`

- [ ] Create a Standard Service Bus namespace, the active `password-sync` queue, and the application safe-DLQ queue. Keep broker-native DLQ behavior distinct from the application safe-DLQ queue; configure the active queue's message TTL from the shared 300-second input and do not add password-bearing dead-letter payload resources.
- [ ] Select and document the Service Bus network path before apply. If the approved topology requires a private endpoint, create it in a dedicated private-endpoint subnet, link the correct private DNS zone to the ACA VNet, and validate ACA resolution; otherwise retain public access only with managed identity, TLS, and an explicit network-exception record. This decision does not alter the on-premises-to-ACA API path, which remains private over the VPN.
- [ ] Assign the runtime UAMI `Azure Service Bus Data Sender` at the active-queue scope and safe-DLQ-queue scope, and `Azure Service Bus Data Receiver` at the active-queue scope. The receiver role also authenticates the ACA queue-depth scaler. Do not grant Data Owner, namespace management, or any SAS authorization rule.
- [ ] Export namespace ID/name/FQDN plus both queue IDs/names. ACA must use the namespace FQDN for `SERVICEBUS_NAMESPACE_FQDN` and the active queue ID for ordering the scale-rule role assignment.
- [ ] Record that Azure role assignments can take several minutes to propagate; deployment verification must wait/retry instead of falling back to a connection string.
- [ ] Run `terraform -chdir=deploy/terraform fmt -recursive` and `terraform -chdir=deploy/terraform validate` after module wiring exists.
- [ ] Commit: `feat(terraform): provision Service Bus with managed identity RBAC`.

### Task 4: Key Vault Module with RBAC-Only Secret Access

**Files:**
- Modify: `deploy/terraform/modules/keyvault/main.tf`
- Create: `deploy/terraform/modules/keyvault/variables.tf`, `deploy/terraform/modules/keyvault/outputs.tf`

- [ ] Create a standard Key Vault with soft-delete retention and purge protection, explicitly enabling Azure RBAC authorization. Use the normalized globally safe name from the root.
- [ ] Select and document the Key Vault network path before apply. If the approved topology requires a private endpoint, create it in the private-endpoint subnet and link the corresponding private DNS zone to the ACA VNet; otherwise retain public access only with managed identity and an explicit network-exception record. Do not grant public network access merely to compensate for missing ACA DNS or VPN routes.
- [ ] Assign the runtime UAMI `Key Vault Secrets User` at the vault scope. For each explicitly supplied operator object ID, assign `Key Vault Secrets Officer`; do not grant operators broad Key Vault Administrator by default.
- [ ] Keep the expected runtime secret names as non-sensitive metadata only: HMAC, Graph client secret, and password payload encryption key. Do not create `azurerm_key_vault_secret`, `azapi` secret resources, or placeholders with values.
- [ ] Export the vault ID/name/URI and expected secret names. Validate that Terraform's deployment principal has sufficient role-assignment authority before apply; do not solve insufficient permissions by weakening the vault access model.
- [ ] Run `terraform -chdir=deploy/terraform fmt -recursive` and module/root validation.
- [ ] Commit: `feat(terraform): provision Key Vault with RBAC secret access`.

### Task 5: Azure Managed Redis Module

**Files:**
- Create: `deploy/terraform/modules/redis/main.tf`, `deploy/terraform/modules/redis/variables.tf`, `deploy/terraform/modules/redis/outputs.tf`

- [ ] Use `azurerm_managed_redis`, not `azurerm_redis_cache`: Azure Cache for Redis SKUs are retired for new deployments. Create the `default_database` with encrypted client protocol, access-key authentication disabled, a non-clustered policy suitable for this small shared-state workload, `NoEviction`, and the approved production-availability setting.
- [ ] Before selecting the default SKU, verify Azure Managed Redis availability, quota, and the supported SKU in the target region. Do not assume the repository's historical `eastasia` default is available for Managed Redis.
- [ ] Create `azurerm_managed_redis_access_policy_assignment` for the runtime UAMI on the default database. The Managed Redis instance is dedicated to this service, so the built-in default data access policy is sufficient for this slice; do not introduce preview custom ACLs unless the current provider offers them as stable resources.
- [ ] Use the normalized unique name and expose only the hostname, TLS port, resource ID, and access-policy assignment ID. Do not read, enable, or output database access keys.
- [ ] Select and document the Managed Redis network path before apply. If the approved topology requires a private endpoint, create it in the private-endpoint subnet, link the correct private DNS zone to the ACA VNet, and test Entra-authenticated TLS connectivity from ACA; otherwise retain public access only with TLS and Entra authentication and an explicit network-exception record. Do not treat the public endpoint as an implicit fallback when private DNS or routing is incomplete.
- [ ] Treat the Redis data as shared operational state rather than an authoritative audit record: it survives app revisions and replica changes, but a Redis-loss fail-open can cause a duplicate Graph sync. Do not promise permanent historical retention or write password material, queue messages, HMAC material, Graph secrets, or raw UPNs to it.
- [ ] Run `terraform -chdir=deploy/terraform fmt -recursive` and `terraform -chdir=deploy/terraform validate`.
- [ ] Commit: `feat(terraform): add Azure Managed Redis sync-status store`.

### Task 6: Container Apps, Existing Application Gateway, ACR, and Azure Monitor Module

**Files:**
- Modify: `deploy/terraform/modules/aca/main.tf`
- Create: `deploy/terraform/modules/aca/variables.tf`, `deploy/terraform/modules/aca/outputs.tf`
- Create: `deploy/terraform/modules/applicationgateway/main.tf`, `deploy/terraform/modules/applicationgateway/variables.tf`, `deploy/terraform/modules/applicationgateway/outputs.tf`

- [ ] Create Log Analytics, an Application Insights instance, and an internal Container Apps environment integrated with the approved ACA infrastructure subnet. Read the approved existing ACR; do not create, administer, or delete it. Assign the runtime UAMI `AcrPull` at that ACR scope before enabling a Container App revision, and validate that the registry permits managed-identity image pulls.
- [ ] Configure the managed OpenTelemetry agent using `azapi_update_resource` on the managed environment, with Application Insights as the logs/traces destination. Use the Application Insights connection string generated by Terraform; it is not a runtime credential. Preserve the Log Analytics configuration owned by the AzureRM environment resource and avoid putting a workspace shared key in a Terraform variable or output.
- [ ] Create the Container App only when `deploy_container_app` is true. Attach the runtime UAMI, use it for existing-ACR registry auth, configure internal HTTPS ingress on target port 8080 (`external = false`), single revision mode, 0.5 vCPU/1 GiB defaults, and explicit HTTP startup/readiness/liveness probes against `/healthz`. Do not create a public ingress endpoint.
- [ ] Configure ACA's default/internal backend hostname, certificate, health probe, and host-header behavior for Application Gateway. The gateway backend setting must use HTTPS, validate the backend certificate chain/SNI, send the host header ACA expects, and probe `/healthz`; do not downgrade the gateway-to-ACA hop to HTTP merely to resolve a probe or hostname issue.
- [ ] Extend the approved existing WAF_v2 Application Gateway in place, using the owner-approved deployment state/pipeline. Add one static private frontend IP, one HTTPS listener bound only to that private frontend and the approved password-hook hostname/certificate, one backend pool/settings/probe for ACA, and one unique-priority routing rule. Create and associate a listener-specific password-hook WAF policy in Prevention mode, with a source-IP allow rule restricted to portal web-server CIDRs before general rules. Do not change any existing public frontend, listener, routing rule, backend, certificate binding, WAF rule/policy, SKU, autoscale setting, or lifecycle state.
- [ ] Configure split-horizon/on-premises DNS for the approved password-hook hostname to return the Application Gateway static private frontend IP. Confirm portal web servers have a VPN route to that IP on TCP 443, but no direct route/firewall permission to the ACA ingress IP. Link Azure private DNS required by the Application Gateway backend to its VNet or configured Azure-side resolver; do not make the ACA default domain a portal-facing DNS dependency.
- [ ] Set non-secret runtime configuration: `SECRETS_SOURCE=keyvault`, Key Vault URI and three secret-name settings, `SERVICEBUS_AUTH_MODE=managed_identity`, namespace FQDN/queue names, Redis store mode/host/TLS port/key prefix, domains, Graph tenant/client IDs, encryption key ID, portal protection settings, and Azure Monitor exporter/metrics values. Omit `SERVICEBUS_CONNECTION_STRING`, its Key Vault secret-name setting, `REDIS_PASSWORD`, Redis access keys, and `OTEL_EXPORTER_OTLP_ENDPOINT`.
- [ ] Use the Container App resource as the Azure Monitor custom-metrics resource. Construct its stable ARM ID from subscription/resource group/app name (not a self-reference), pass that ID and location/namespace to the app, and assign `Monitoring Metrics Publisher` to the UAMI at that Container App scope.
- [ ] Add the Service Bus `azure-servicebus` custom scale rule with the active queue name, namespace name, `messageCount = "50"`, and the UAMI `identity_id`. Do not use scaler secrets or connection strings. Verify this exact provider field against the pinned AzureRM schema before apply.
- [ ] Export the Container App ID, private API hostname, Application Gateway private frontend/listener/rule IDs, ACA environment static ingress IP/ID, existing ACR server/resource ID, Log Analytics and Application Insights resource IDs, and the selected metrics resource ID. Do not describe the API as publicly reachable.
- [ ] Run `terraform -chdir=deploy/terraform fmt -recursive`, `terraform -chdir=deploy/terraform init -backend=false`, and `terraform -chdir=deploy/terraform validate`.
- [ ] Commit: `feat(terraform): add private Application Gateway path to Container Apps`.

### Task 7: Deployment and Operator Documentation

**Files:**
- Create: `deploy/terraform/README.md`
- Modify: `README.md`

- [ ] Document the deployment sequence: apply with `deploy_container_app=false` to establish the selected network/DNS dependencies and runtime identity/RBAC; build and push the image to the approved existing ACR; inject the HMAC, Graph client secret, and password-encryption key directly into Key Vault with an approved secret-handling process; deploy the staging Application Gateway private frontend/listener/WAF/backend path through its owner-approved state/pipeline; then apply with `deploy_container_app=true`. Promote the same isolated change to production only after staging verification. Never include actual secret values, payloads, VPN shared keys, or shell-history-prone examples in Terraform output.
- [ ] Document production identity flow: UAMI reads Key Vault, sends/receives Service Bus messages, drives the KEDA scaler, publishes custom metrics, and authenticates to Azure Managed Redis through Entra ID. State clearly that Redis does not use an access key and Service Bus does not use a connection string in production.
- [ ] Document the Redis state model and retention: shared external state preserves dedupe across ACA replicas/revisions; it is not a password store or historical system of record; a Redis outage/loss fails open and can cause a duplicate password sync. Explain the hashed-key privacy boundary and the terminal/pending TTL behavior.
- [ ] Document the on-premises portal-web-server walkthrough as a production gate: establish the S2S VPN and routes; configure split-horizon/on-premises DNS so the password-hook hostname returns the Application Gateway private frontend IP; verify TLS/SNI and `GET /healthz` from every portal web server; inject the private HTTPS API URL and HMAC secret through the approved on-premises application configuration/secret mechanism; then submit one signed staging hook request and verify `202 Accepted`, WAF decision, Application Gateway backend health, queue processing, and a password-safe log/metric trail. The walkthrough must use placeholders and redacted outputs only.
- [ ] Add source-address validation as a deployment gate: before accepting production portal CIDRs, send a signed staging request from each portal web server through the VPN and record the WAF/application-observed peer addresses. If the Application Gateway or ACA path does not preserve an address that can be safely matched to the approved portal CIDR, stop before rollout and create a focused follow-up to define trusted-proxy handling; do not silently switch to client-controlled `X-Forwarded-For`.
- [ ] Update root README infrastructure and configuration sections so they reflect private Application Gateway WAF_v2 ingress over S2S VPN, split-horizon DNS/certificate prerequisites, listener-specific WAF restrictions, internal ACA backend TLS, the existing ACR, managed-identity Service Bus, Azure Managed Redis sync status, observability config, and the non-secret Terraform workflow. Remove the statement that Terraform is merely a later slice.
- [ ] Commit: `docs: document infrastructure and shared sync-status deployment`.

### Task 8: Verification, State-Safety Review, and Handoff

**Files:**
- Verify: `internal/config/**`, `internal/syncstatus/**`, `internal/app/**`, `deploy/terraform/**`, `README.md`

- [ ] Run `gofmt -w` only on changed Go files, then `go test ./internal/config ./internal/syncstatus ./internal/migration ./internal/worker ./internal/app`, `go test ./...`, and `go vet ./...`.
- [ ] Run `terraform -chdir=deploy/terraform fmt -recursive -check`, `terraform -chdir=deploy/terraform init -backend=false`, and `terraform -chdir=deploy/terraform validate`. Run a non-production `terraform plan` using the example tfvars after provider login is available; inspect that access keys and runtime secrets are absent from the plan.
- [ ] Verify the hybrid API path in a non-production environment from every on-premises portal web server: the private hostname resolves through split-horizon/on-premises DNS to the approved Application Gateway private frontend IP; TCP 443 and TLS/SNI succeed through the site-to-site VPN; the WAF allow rule matches the approved source; Application Gateway reports the ACA backend healthy; `/healthz` responds successfully; one signed hook request receives `202 Accepted`; and the application-observed source address matches the approved allowlist. Confirm that the portal source cannot directly reach the ACA ingress IP. Capture only redacted command outcomes and resource IDs, never request bodies, passwords, HMAC material, signatures, nonces, VPN pre-shared keys, or tokens.
- [ ] Before and after every staging/prod Application Gateway update, capture the existing frontend/listener/rule/backend/WAF summary, provisioning state, and synthetic health checks for unrelated public routes. Roll back by removing only the password-hook private frontend/listener/rule/backend/WAF resources through the gateway owner's approved state/pipeline; never restore the entire gateway from an unreviewed configuration snapshot.
- [ ] Verify the selected PaaS network paths from ACA: ACR image pull through the runtime UAMI, Key Vault secret read, Service Bus send/receive and scale-rule authentication, and Redis Entra/TLS connectivity. For every service selected as private, verify private DNS resolution and absence of a public-network fallback; for every approved public exception, verify the exception is documented with owner and review date.
- [ ] Run a state/config safety scan covering Terraform and changed documentation: search for `azurerm_key_vault_secret`, `SERVICEBUS_CONNECTION_STRING`, `servicebus-conn-str`, `REDIS_PASSWORD`, `primary_access_key`, `secondary_access_key`, `SharedAccessKey=`, `HOOK_HMAC_SECRET\s*=`, `GRAPH_CLIENT_SECRET\s*=`, and `PASSWORD_ENCRYPTION_KEY_B64\s*=`. Each expected documentation mention must be non-secret and explicitly local/deprecated where applicable; Terraform code must have no runtime-secret value path.
- [ ] Review the Redis implementation for raw UPNs, password/ciphertext fields, request bodies, Graph payloads, secrets, and tokens. Verify the Lua ordering test and a multi-client integration test prove that an older worker completion cannot overwrite a newer outcome.
- [ ] Run `git diff --check` and inspect `git diff --name-only` against the branch base. Record verification results and unresolved live-Azure prerequisites in the implementation handoff; do not claim staging or production validation without actually performing it.
- [ ] Commit any final formatting/docs corrections separately: `chore: verify Slice 10 infrastructure`.

## Completion Criteria

- The application uses a shared Azure Managed Redis store under managed identity, and the same store instance is wired into both hook and worker paths.
- Terraform creates or consumes the approved VNet/S2S VPN/private-DNS resources, creates internal ACA, extends the approved existing WAF_v2 Application Gateway only with an isolated private password-hook path, uses the approved existing ACR, creates Service Bus, Key Vault RBAC, Managed Redis, Application Insights/Log Analytics, UAMI role assignments, telemetry agent configuration, and Service Bus queue-depth scaling without Service Bus/Redis secret credentials.
- The bootstrap path uses the existing ACR and is executable without creating an invalid Container App revision.
- All local Go and Terraform validation checks pass, and Terraform/config scans show no runtime secret values or generated credentials.
- Every on-premises portal web server can resolve the private API hostname to the Application Gateway private frontend, reach it over the S2S VPN with validated TLS, and submit an authenticated staging hook request without exposing either the gateway path or ACA publicly.
- Production rollout remains blocked until the Application Gateway/ACA source-address behavior is verified against the portal egress allowlist, all unrelated existing Application Gateway public paths pass regression checks, and every selected PaaS public/private network path is validated.
