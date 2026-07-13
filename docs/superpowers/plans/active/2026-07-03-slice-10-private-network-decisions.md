# Slice 10 Private Network Decisions

> **Status:** Owner decisions finalized; staging preflight, external change coordination, and live validation remain blocking gates.
>
> **Discovery date:** 2026-07-13
>
> **Safety boundary:** Inventory used read-only Azure CLI operations only. No secrets, credentials, access keys, certificate material, VPN shared keys, request bodies, or queue payloads were read or recorded.

## Decision Gate

This document records observed Azure state separately from approved design decisions. Approval of this document authorizes implementation planning, source changes, and staging preflight; it is not authorization to bypass GitHub/Azure environment approvals or perform an unreviewed Azure change. Live staging changes require the named pipeline, technical-team coordination, redacted plan/review, and change window described below.

| Decision | Current disposition | Blocking |
|---|---|---:|
| Subscription and region | Approved: use `LGTW-PoC`, Japan East, and dedicated password-hook resource groups | No for design; staging plan/apply approval still required |
| VNet/VPN ownership | Approved: consume the existing hub VPN and spoke VNets; do not create a parallel VPN or hub | No for design; technical-team change window remains required |
| On-premises-to-Application-Gateway route | Approved: add direct reciprocal AGW-VNet-to-hub peerings, staging first; retain existing AGW-to-workload peerings | No for design; route/selector implementation details remain required |
| Application Gateway ownership | Owner confirmed permission; change must be implemented in `lyy-nycu/ldap-service` and its existing pipeline | No for ownership; repository inspection/change window remain required |
| ACA environment | Reuse the existing environments, staging first and production only after staging validation | No for topology; environment-level telemetry ownership remains pending |
| ACR | Reuse shared Standard `acrjpe001` with a staging-only public endpoint exception owned by the password-hook service owner; review at production readiness and no later than 2026-10-11 | No for staging; production requires a new decision |
| Key Vault | Create dedicated with private endpoint and public access disabled | No for mode; PE/DNS details remain required |
| Service Bus | Create dedicated Premium namespace with private endpoint and public access disabled | No for mode; quota/PE/DNS details remain required |
| Azure Managed Redis | Create dedicated with private endpoint, public access disabled, Entra ID and TLS | No for mode; SKU/quota/PE/DNS details remain required |
| Sync-status terminal TTL | `90d` | No |
| Private API hostname | Staging reuses `api.test.nycu.edu.tw` through split-horizon DNS: public frontend remains LDAP, on-premises resolves the private frontend to password-hook | No for hostname/DNS design; certificate pipeline verification remains required |
| Portal caller CIDRs | Staging `140.113.7.17/32`; production `140.113.41.155/32` and `140.113.41.177/32` | No for declared callers; staging WAF observation remains required |
| Trusted proxy CIDRs | Intentionally deferred until staging observes the actual ACA immediate peer and sanitized forwarded chain | Yes—production/runtime gate, not an owner-choice gap |

## Verified Deployment Scope

- Azure subscription name: `LGTW-PoC`
- Subscription ID: `56b72537-d985-4530-88f3-b6ed07e71c67`
- Tenant ID: `7ef65350-5b77-4958-aca5-0ccadb6bd0b7`
- Observed target region: Japan East
- Shared network resource group: `rg-spoke-paas`
- Hub/VPN resource group: `rg-vpngw-jp-001`
- Existing ACR resource group: `rg-acr-jpe-001`

The application resource-group boundary is approved below; subscription/region confirmation and the deployment principal remain outstanding. Existing resource-group names are inventory evidence, not proof that this repository owns their shared resources.

Owner-approved resource-group shape:

- staging application resources: `rg-password-hook-stg-jpe-001`;
- production application resources: `rg-password-hook-prod-jpe-001`;
- existing Gateway, ACA environment, network, and ACR resources remain in their current resource groups and are referenced rather than imported into this repository's state;
- staging is implemented and validated before production.

## Verified Hybrid Network State

| Environment | Workload VNet | ACA subnet | Application Gateway VNet | Gateway subnet |
|---|---|---|---|---|
| Staging | `vnet-stg-jpe-001` (`10.0.4.0/24`) | `cae-stg-jpe-001` (`10.0.4.0/26`) | `vnet-ag-stg-jpe-001` (`10.0.8.0/24`) | `agw-subnet` (`10.0.8.0/26`) |
| Production | `vnet-prod-jpe-001` (`10.0.3.0/24`) | `cae-prod-jpe-001` (`10.0.3.0/26`) | `vnet-ag-prod-jpe-001` (`10.0.9.0/24`) | `agw-subnet` (`10.0.9.0/26`) |

Verified relationships:

- Each workload VNet has a connected peering to its Application Gateway VNet.
- Each workload VNet has a connected peering to the hub, with forwarded traffic and remote-gateway use enabled; the reciprocal hub peerings allow gateway transit.
- The hub contains route-based, active-active `VpnGw2` gateway `vpngw-hub-jp-001`.
- IPsec connection `s2s-az-juniper-jp-001` is provisioned and operationally `Connected`; BGP is disabled and policy-based traffic selectors are enabled.
- The Local Network Gateway advertises multiple campus/on-premises prefixes, but the exact portal-web-server addresses and their route/firewall ownership have not been identified.
- No observed workload or Application Gateway subnet has a route table association. An empty, unattached staging route table exists and is not evidence of an active route.

### Blocking routing gap

Neither Application Gateway VNet has a direct peering to the hub. Each is peered only to its workload VNet, and ordinary VNet peering is not transitive. Therefore the current topology does not establish that on-premises portal servers can reach a new private frontend in `10.0.8.0/24` or `10.0.9.0/24` through the S2S VPN.

Required owner decision:

The owner approved direct reciprocal peerings between each Application Gateway VNet and the hub, staging first, while retaining the existing Application-Gateway-to-workload peerings for ACA backend traffic. The hub side must provide gateway transit; the AGW VNet side must use the remote gateway. Before apply, validate that this VNet has no conflicting remote-gateway use, update the on-premises route/traffic selectors, and prove both forward and return paths. Production follows only after staging succeeds.

The owner must also confirm that only TCP 443 from the approved portal-server CIDRs reaches the selected private frontend and that no portal route/firewall rule permits direct access to the ACA environment ingress IP.

### Additional network checks

- Both Gateway subnets currently have no `Microsoft.Network/applicationGateways` delegation. The gateway/network owner must validate and apply current delegation requirements before adding the private frontend.
- The staging ACA subnet and staging Gateway subnet reference the same NSG name. The owner must confirm this is intentional and assess changes without broadening either tier.
- All observed VNets use Azure-provided DNS. Staging has a Private DNS Resolver with inbound and outbound endpoints, but forwarding ruleset links/rules and the on-premises DNS owner remain unverified.
- Existing private DNS zones cover LDAP and production SQL only. No observed zones support Container Apps, ACR, Key Vault, Service Bus, or Managed Redis private endpoints.

## Existing Application Gateway Contract

Both observed gateways are running WAF_v2 resources in Japan East with fixed capacity `1`, successful provisioning, and one public IPv4 frontend. Neither currently has a private frontend.

| Environment | Gateway | Existing workload summary | WAF state |
|---|---|---|---|
| Staging | `rg-spoke-paas/agw-stg-jpe-001` | 8 listeners, 7 rules, several shared backends/probes; active automation writes are visible | Gateway policy enabled in Detection; a listener-specific policy also uses Prevention |
| Production | `rg-spoke-paas/agw-prod-jpe-001` | Existing production HTTP/HTTPS listeners and ACA backend | Gateway policy enabled in Detection |

Ownership findings and decision:

- Tags do not identify an owner.
- Staging has recent near-daily writes by the `id-ldap-service-cicd` managed identity plus human operators. Production also has recent human writes.
- The gateways are actively shared and reconciled. This repository must not import them or declare a partial/full `azurerm_application_gateway` resource in a separate Terraform state.
- The service owner confirmed they have permission to manage both gateways and identified `lyy-nycu/ldap-service` as the existing automation repository. The password-hook additions must be implemented through that repository's existing state/pipeline—especially staging—rather than through an independent competing configuration. Before any write, inspect its current source/state boundary and pull-request/change-window requirements. The pipeline must return the deployed resource IDs and redacted verification result.

The owner-approved handoff must specify:

- one unused static private frontend IP in the existing Gateway subnet;
- the private API hostname and listener certificate reference/owner;
- a unique listener and rule priority that does not collide with existing rules;
- ACA backend FQDN, HTTPS port, validated certificate chain, SNI and host-header behavior;
- `/healthz` probe settings;
- a listener-specific WAF policy in Prevention mode and the exact portal source CIDRs;
- before/after health checks for unrelated routes and rollback of only the new isolated objects.

Staging requires particular care because it is multi-tenant and actively reconciled. Production's existing gateway-level WAF policy is in Detection mode, so the owner must confirm that the listener-specific Prevention policy is supported and intentionally isolated rather than silently changing the existing gateway policy.

## ACA and Observability Boundary

Existing internal environments were discovered:

| Environment | Container Apps environment | Network | Log Analytics |
|---|---|---|---|
| Staging | `rg-cae-stg-jpe-001/cae-stg-jpe-001` | Internal, public access disabled, static IP `10.0.4.55` | Existing workspace, 30-day retention |
| Production | `rg-cae-prod-jpe-001/cae-prod-jpe-001` | Internal, public access disabled, static IP `10.0.3.15` | Existing workspace, 30-day retention |

The owner selected reuse of both existing environments, with staging implemented and validated before production. This avoids new ACA environment/subnet construction and matches existing Gateway-to-ACA patterns. Environment-level managed OpenTelemetry or Log Analytics changes still have a shared blast radius and require verification against current environment configuration before implementation.

Terraform must consume the existing staging/prod environment IDs rather than declare or import the shared environments. This repository may deploy the Container App and app-scoped resources through an owner-approved integration. Environment-level telemetry changes remain with the existing environment owner/change mechanism and must be performed staging-first.

## PaaS Endpoint Matrix

No existing Service Bus namespace, Azure Managed Redis/Redis Enterprise instance, or Application Insights component was found in the subscription. No password-hook-specific Key Vault was identified. Dedicated resources are therefore the proposed application boundary, subject to resource-group and endpoint approval.

| Service | Verified current state | Proposed disposition | Required approval/work |
|---|---|---|---|
| ACR | Candidate `rg-acr-jpe-001/acrjpe001`; Standard; public access enabled; no PE; admin/anonymous access disabled | Reuse with the approved staging-only public endpoint exception | Runtime UAMI `AcrPull` and ACA egress validation. Review at production readiness or 2026-10-11, whichever occurs first. A private choice requires Premium/Private Link design and is not selected for staging. |
| Key Vault | No password-hook vault | Create dedicated with private endpoint and public access disabled | PE subnet, `privatelink.vaultcore.azure.net` zone/link owner, ACA DNS test, deployment principal/RBAC owner |
| Service Bus | None found | Create Premium dedicated namespace with private endpoint and public access disabled | PE subnet, `privatelink.servicebus.windows.net` zone/link owner, ACA DNS test, Premium quota/cost/provider validation |
| Azure Managed Redis | None found | Create dedicated with private endpoint, public access disabled, Entra ID and TLS | Japan East SKU/quota/provider validation, current Managed Redis PE DNS zone confirmation, PE subnet/link owner, Entra/TLS test |
| Application Insights | None found | Create password-hook-specific component; reuse the existing staging-first Log Analytics/ACA boundary | Existing ACA owner pipeline must configure and validate the managed OTel destination without declaring the shared environment in this repository |

Terraform must implement exactly the approved row and must not include an implicit public fallback. The ACR public endpoint exception is staging-only, owned by the password-hook service owner, and expires at the production-readiness review or 2026-10-11, whichever occurs first; production must make a new endpoint decision.

Only one existing private endpoint was observed, for production SQL. There is no reusable general private-endpoint subnet or DNS-zone set established by this inventory; the network owner must designate both before private PaaS module work begins.

The owner approved creating a dedicated private-endpoint subnet and the required Private DNS zones/links in staging first, followed by production after validation. Exact non-overlapping subnet CIDRs, DNS-zone resource groups, and link ownership still require address-capacity and existing-DNS-state validation.

## Private API DNS, TLS, and Caller Contract

The following values remain owner inputs and must not be guessed from existing public services:

- approved private API hostname;
- Application Gateway listener certificate trust chain and verified renewal behavior after the private-listener binding is added;
- exact staging and production portal-web-server source CIDRs as observed by WAF;
- on-premises secret-store/configuration owner for the HMAC secret and private API URL;
- firewall owner for portal-to-private-frontend TCP 443;
- staging synthetic-request owner and password-safe verification procedure.

The owner identified the technical team as responsible for the on-premises split-horizon DNS record and its change/rollback. The exact named operator/change window must be recorded before the staging DNS update.

Verified existing hostname state:

- `api.test.nycu.edu.tw` resolves publicly to the staging Application Gateway public frontend.
- It is already bound to public HTTP and HTTPS listeners and a path-based rule whose default backend is the LDAP service; its ACME challenge path is also part of that routing contract.
- The owner selected reuse of `api.test.nycu.edu.tw` for the higher-priority staging password-hook service. Split-horizon DNS will return the new private frontend IP to approved on-premises clients while public DNS continues to return the public frontend for LDAP.

Required isolation and regression conditions for this shared hostname:

- Bind a new HTTPS listener for `api.test.nycu.edu.tw` only to the private frontend; do not alter the existing public listeners or LDAP path map.
- Route the private listener only to the password-hook ACA backend and attach its listener-specific WAF policy. Do not expose the password-hook backend/path through the public listener.
- Reuse the existing certificate only after verifying its SAN, trust chain, renewal automation, and ability to bind to both listeners without changing the public ACME challenge route.
- The current certificate is issued through ACME HTTP-01. Keep challenge resolution on the public DNS/public frontend and existing ACME path; the private listener must not intercept or duplicate the challenge. Verify that `lyy-nycu/ldap-service` renewal updates the same Application Gateway certificate resource and preserves both public and private listener bindings instead of deleting/recreating a certificate or listener incompletely.
- Read-only verification on 2026-07-13 confirmed the public endpoint presents a Let's Encrypt certificate with `DNS:api.test.nycu.edu.tw`, valid from 2026-07-12 through 2026-10-10; the available system trust store validated the served chain successfully. Trust still must be tested from every actual portal server.
- The `lyy-nycu/ldap-service` `acme-renew-staging.yml` workflow imports the renewed PFX into Key Vault, resolves the new secret-version ID, upserts a fixed-name Application Gateway SSL certificate reference, and updates one configured listener; it does not intentionally delete/recreate the listener. The latest ten scheduled runs observed through 2026-07-12 all succeeded.
- Because the workflow currently accepts and verifies only one `AGW_LISTENER_NAME`, extend it before adding the private listener: verify both public and private listeners reference the fixed `AGW_SSL_CERT_NAME` after every renewal. Rebinding the private listener should not be necessary when both listeners reference the same fixed certificate object, but the workflow must fail if either binding is lost.
- The scheduled workflow uses a fresh Certbot configuration directory and runs `certbot certonly` daily; the served certificate's 2026-07-12 issuance date matches that behavior. Before relying on it for the shared private listener, change the workflow to renew only within an approved expiry window and preserve renewal state, or document why the configured ACME server safely supports daily reissuance. This avoids unnecessary certificate churn and CA rate-limit risk.
- The owner confirmed no staging or production on-premises service depends on `ldap-service`. The private listener therefore belongs exclusively to password-hook and must not preserve LDAP path routing. Public DNS/listeners continue to serve LDAP unchanged.
- Test public DNS/public LDAP routing before and after the change, plus private DNS/password-hook routing from every approved portal server. Rollback changes only the on-premises DNS record and isolated private listener objects.

Public authoritative DNS must not publish the private frontend address. From every portal web server, the deployment gate must verify private resolution, TLS hostname/SNI, `/healthz`, a signed staging request returning `202`, WAF decision, backend health, queue processing, and absence of sensitive request material in output/logs.

## Trusted Proxy Inputs

The application currently uses `RemoteAddr` and deliberately ignores `X-Forwarded-For`; Task 0A must be completed before this topology can enforce portal CIDRs behind Application Gateway/ACA.

`TRUSTED_PROXY_CIDRS` must be derived from the actual immediate peers observed at the Container App, not from the portal CIDRs or broad VNet ranges. Staging verification must record only a sanitized hop shape and prove:

- the immediate peer is inside the approved trusted-proxy boundary;
- the resolved client address matches the WAF-observed portal source;
- an untrusted direct peer cannot spoof a forwarded header;
- distinct portal clients behind one proxy receive independent rate-limit buckets.

If the observed chain cannot be validated unambiguously, rollout stops. The remedy is a revised trusted-proxy design, not a broader CIDR or unconditional trust of the leftmost forwarded address.

## Sync-Status Retention Decision

The owner selected a terminal TTL of `90d` and acknowledged that expiration changes a terminal account back to the effective unknown/unsynced state, allowing a later `login_bootstrap` to enqueue another Graph sync. This retention is separate from the five-minute pending/message TTL.

## Deployment Identity Decision

The owner approved using the currently authenticated user identity for the first staging Terraform plan/apply, with the exact actor and redacted plan result recorded. This is a staging bootstrap choice, not the long-term production model.

The preferred long-term model is a dedicated pipeline workload identity or service principal with federated credentials, least-privilege resource/RBAC scopes, approval gates, and auditable plan/apply runs. Do not retain a personal user identity as the unattended production deployment principal.

## Final Approved Staging Configuration

The following values are the implementation baseline. Changing one requires updating this decision document before apply.

| Area | Approved staging value |
|---|---|
| Rollout | Staging first; production only after all staging gates pass |
| Application resource group | `rg-password-hook-stg-jpe-001` |
| ACA | Reuse `rg-cae-stg-jpe-001/cae-stg-jpe-001`; reference it as existing and do not import/manage the shared environment in password-hook state |
| Client route | Existing S2S VPN and hub; add reciprocal hub-to-AGW-VNet peering with gateway transit/remote gateway; retain existing AGW-to-workload peering |
| Portal source | `140.113.7.17/32` |
| Private API hostname | `api.test.nycu.edu.tw` through on-premises split-horizon DNS |
| Gateway private frontend | `10.0.8.62` in `vnet-ag-stg-jpe-001/agw-subnet`; re-check availability immediately before change |
| Gateway rule priority | `120`; re-check collision immediately before change |
| Gateway change owner | `lyy-nycu/ldap-service`, dedicated manually approved staging workflow |
| Listener routing | Private frontend/listener routes only to password-hook; existing public LDAP listeners/path map and public ACME challenge remain unchanged |
| WAF | Listener-specific Prevention policy; OWASP 3.2 plus BotManager 0.1; priority-10 negated `RemoteAddr IPMatch` Block rule for every source except `140.113.7.17/32`; do not use an Allow rule that bypasses managed inspection |
| Private Endpoint subnet | `snet-pe-password-hook-stg-jpe-001`, `10.0.4.224/27`, no delegation/service endpoint, no reuse of the shared AGW/ACA NSG |
| Private DNS ownership | Central network/DNS owner state in `rg-spoke-paas`; link staging VNet now and production only during production rollout |
| Private DNS zones | `privatelink.vaultcore.azure.net`, `privatelink.servicebus.windows.net`, `privatelink.redis.azure.net` |
| ACR | Reuse Standard `acrjpe001` with UAMI `AcrPull`; staging-only public endpoint exception until production readiness or 2026-10-11, whichever occurs first |
| Key Vault | Dedicated, RBAC-only, private endpoint, public access disabled; only HMAC, Graph client secret, and password-encryption key values are operator-injected |
| Service Bus | Premium, one partition, one messaging unit, private endpoint, public access disabled, local/SAS auth disabled, TLS 1.2+ |
| Azure Managed Redis | `Balanced_B0`, HA enabled, `OSSCluster`, no persistence, `NoEviction`, private endpoint, public access/access-key auth disabled, Entra ID and TLS |
| Redis client implication | Use `go-redis` ClusterClient; each Lua transition operates on one digest key/hash slot |
| Sync-status retention | Pending/message TTL `5m`; terminal TTL `90d` |
| Secret administration | Portal never connects to Azure Key Vault. Prefer a protected Azure-connected self-hosted runner for Key Vault injection/rotation after its private DNS/TCP 443 path is verified; otherwise use an explicitly approved private management path |
| Initial Terraform identity | Current authenticated user for the first reviewed staging plan/apply |
| Long-term Terraform identity | Dedicated federated pipeline workload identity/service principal with least privilege and approval gates |

Recommended Gateway object names:

- frontend IP configuration: `feip-password-hook-stg-private`;
- reuse frontend port: `port_443`;
- listener: `listener-password-hook-stg-private-https`;
- backend pool: `pool-password-hook-stg`;
- backend HTTPS settings: `set-password-hook-stg-https`;
- probe: `probe-password-hook-stg-healthz`;
- rule: `rule-password-hook-stg-private`;
- WAF policy: `waf-policy-password-hook-stg`.

## Work Split for a Fresh Agent Session

Use sub-agents only for bounded read-only or source-review work; the primary agent owns decisions, cross-checks raw evidence, writes each plan/task section incrementally, and integrates changes. Recommended order:

1. **Primary agent — staging preflight coordinator:** reread `AGENTS.md`, this decision document, the active plan, and the exact source files for the next task; maintain the dependency gate and never start later work before preceding checks pass.
2. **Network/DNS sub-agent — medium complexity, read-only first:** re-check subnet/IP/priority availability, provider/SKU capacity signals, peerings, DNS zones, and runner private connectivity; return evidence only. Azure writes remain with the primary agent through reviewed workflows/plans.
3. **Gateway/ACME sub-agent — high complexity:** work in `lyy-nycu/ldap-service`; inspect its repository instructions and PR template, then propose the dedicated staging workflow plus ACME renewal/binding fixes. Preserve existing LDAP routes and test rollback/regression. Do not expose certificate material.
4. **Task 0A application sub-agent — high security complexity:** implement trusted-proxy source resolution only after the primary agent verifies the current password-hook source; include spoofing, malformed-chain, IPv4/IPv6, and independent-rate-bucket tests. Do not guess runtime trusted CIDRs.
5. **Redis application sub-agent — high concurrency complexity:** implement strict config, ClusterClient/Entra/TLS runtime, one-key Lua precedence, lifecycle cleanup, and memory/Redis parity. The primary agent reviews state-machine and privacy semantics.
6. **Terraform module sub-agents — medium/high complexity, one module at a time:** after Task 0A/Redis checks pass, implement root/network references, Service Bus, Key Vault, Redis/PE, then ACA/observability. No two agents edit the same root/module files concurrently.
7. **Primary agent — integration and handoff:** run all Go/Terraform verification, state/secret scans, staging plan review, and external-owner coordination. Never claim live validation before it occurs.

Model allocation, when the client supports it:

- use the strongest reasoning model for gateway/network ownership, trusted proxies, Redis concurrency/Lua semantics, Terraform integration, and final review;
- use a faster/lower-cost model for bounded inventory projections, documentation consistency, naming collision scans, and mechanical test enumeration;
- require the primary/strong model to inspect original outputs before accepting any lower-cost model conclusion.

## Remaining Staging Gates

- [x] Subscription, dedicated resource groups, Japan East region, initial user deployment identity, and long-term federated pipeline identity direction approved.
- [x] Existing hub/VPN consumption and direct reciprocal AGW-to-hub peering design approved; retain AGW-to-workload peering.
- [ ] Technical-team/network change owners and windows recorded for peering, on-premises route/traffic-selector, firewall, and split-horizon DNS changes.
- [ ] `lyy-nycu/ldap-service` dedicated manual workflow reviewed: re-check private IP `10.0.8.62`, priority `120`, isolated objects/WAF, certificate binding, before/after public LDAP checks, and rollback.
- [x] Existing ACA model selected: reuse staging then production, reference the shared environments, and keep environment-level OTel/Log Analytics changes in their existing owner pipeline.
- [x] ACR `acrjpe001` approved for staging with a public endpoint exception owned by the password-hook service owner; review at production readiness or 2026-10-11, whichever occurs first.
- [x] Private Endpoint subnet `10.0.4.224/27`, central Private DNS zone ownership, Key Vault private, Service Bus Premium private, Managed Redis private, and monitoring modes approved.
- [ ] Re-check Service Bus Premium/Managed Redis Japan East capacity during staging preflight; stop rather than introduce a public or legacy-SKU fallback if allocation fails.
- [x] Managed Redis `Balanced_B0`, HA enabled, GA `OSSCluster`, Entra/TLS, and application ClusterClient implication approved.
- [ ] Staging hostname approved as split-horizon `api.test.nycu.edu.tw`; SAN, served chain, renewal source, and recent runs are verified. Actual portal-server trust plus workflow preservation/verification of both listener bindings remain outstanding.
- [ ] Verify the protected self-hosted runner resolves and reaches the Key Vault private endpoint before selecting it as the secret injection/rotation path; portal servers never receive Key Vault connectivity.
- [ ] Implement Task 0A and measure the actual ACA immediate peer/forwarded chain in staging before setting production `TRUSTED_PROXY_CIDRS`.
- [x] Sync-status terminal TTL approved as `90d`.

Owner decision discussion is complete. Unchecked boxes are staging preflight/live-validation gates, not invitations to redesign the approved topology. Terraform validation or the presence of existing resource IDs does not constitute deployment approval.

## Read-Only Evidence Collected

Discovery used safe-field projections from `az account show`, resource-group/resource lists, VNet/subnet/peering queries, VPN Gateway/Local Network Gateway/VPN connection queries, route-table and Private DNS/Resolver queries, Application Gateway/WAF policy and activity-log queries, ACR metadata/RBAC queries, Container Apps environment queries, Log Analytics queries, and private-endpoint lists.

Azure Resource Graph was unavailable in the installed CLI, so direct resource APIs were used. The inventory is scoped evidence, not a guarantee that no separately owned resource exists outside the queried subscription and resource groups.
