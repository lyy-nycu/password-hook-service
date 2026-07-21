# ADR 2026-07-21: Container App Must Use External Ingress to Be an Application Gateway Backend

## Status

Accepted (implemented in staging; `deploy/terraform/modules/aca/main.tf`)

## Context

Slice 10 fronts `password-hook-service` with the shared Application Gateway
(`agw-stg-jpe-001` / `agw-prod-jpe-001`), which reaches the Container App over
its Azure Container Apps (ACA) ingress FQDN. The original design set the
Container App's ingress to `external_enabled = false` ("internal"), on the
theory that this was strictly more secure than `external_enabled = true`: the
shared ACA environment already has no public inbound IP
(`vnetConfiguration.internal = true`), and an additional app-level "internal"
flag looked like defense-in-depth on top of that.

After deploying staging infrastructure (Pass 1 + Pass 2) and the Application
Gateway backend objects (`lyy-nycu/ldap-service` PRs #19/#20), the AGW backend
health for `pool-password-hook-stg` stayed **Unhealthy** and never resolved
past **404** ("Received invalid status code: 404 in the backend server's HTTP
response"), even after independently confirming and fixing two real, separate
issues along the way:

- A missing `*.internal` wildcard A record in the private DNS zone (the zone
  only had a single-level `*` record, which does not match the two-label
  `<app>.internal.<domain>` pattern that internal-ingress apps use). Fixing
  this made DNS resolve correctly, and AGW's probe result changed from
  `Unknown` to a concrete `Unhealthy (404)` — proof the network path (DNS,
  TCP, TLS) was working.
- Backend HTTP settings/probe host-header configuration was switched to
  `pickHostNameFromBackendAddress` / `pickHostNameFromBackendHttpSettings`,
  matching both the community-documented fix for this exact scenario
  ([Stack Overflow](https://stackoverflow.com/questions/70977973/azure-application-gateway-received-invalid-status-code-404-from-app-service))
  and Microsoft's own tutorial for this integration
  ([Protect Azure Container Apps with Application Gateway and WAF](https://learn.microsoft.com/en-us/azure/container-apps/waf-app-gateway)).

The 404 persisted regardless. Ruling out an application-level bug was
straightforward: `internal/httpserver/server.go`'s `/healthz` handler is
registered with no host-based routing and no middleware, so it unconditionally
returns `200` for any request that reaches the Go process — the 404 could only
be happening before the request reached our container.

The actual root cause was found in Microsoft's ingress documentation
([Ingress in Azure Container Apps](https://learn.microsoft.com/en-us/azure/container-apps/ingress-overview#external-and-internal-ingress)):

> **Internal**: Makes the app reachable only from within the same Container
> Apps environment, such as from other container apps. The app isn't
> directly accessible from the public internet.

"Internal" ingress is scoped to **app-to-app calls within the same ACA
environment**. Application Gateway, even though network-adjacent (same/peered
VNet, resolving the same private DNS zone), is not "another container app in
the same environment" — so ACA's ingress edge legitimately rejects the
request with a 404, independent of DNS, TLS, host header, or WAF
configuration.

Empirical confirmation from the shared environment (`cae-stg-jpe-001`) itself:
every other Application Gateway backend pool on `agw-stg-jpe-001` points at an
`external_enabled = true` app FQDN (`ldap-service-staging`, `portal-backend-go`,
`ca-pr-frontend-stg-jpe-001`, `ca-prevention-fe-stg-jpea-001`, ...) and all of
them report `Healthy`. The one app in that environment that does have
`external_enabled = false` ingress (`ca-prevention-be-stg-jpea-001`) is **not**
exposed through the Application Gateway at all — only its `external_enabled =
true` counterpart (`ca-prevention-fe-stg-jpea-001`) is. This is exactly the
intended internal-ingress usage pattern (an internal backend app called only
by its own environment's frontend app), and it confirms no one had previously
attempted to put this Application Gateway in front of an internal-ingress ACA
app before.

## Decision

Set `ingress.external_enabled = true` on the `password-hook-service` Container
App (`deploy/terraform/modules/aca/main.tf`).

The real security boundary is unchanged by this: the shared ACA environment
itself has no public inbound IP (`vnetConfiguration.internal = true`,
confirmed via `az containerapp env show` -> `staticIp` is an RFC 1918
address). `external_enabled = true` only makes the app reachable via that same
private static IP within the environment's own VNet context — it does not by
itself create any public exposure. The app-level ingress flag was never doing
independent security work in this topology; it only controls whether an
external reverse proxy (like this Application Gateway) is allowed to route to
the app at all.

Applied to staging via Terraform (in-place update on the existing Container
App resource, no downtime), plus a one-time manual fix of the already-created
Application Gateway backend pool member address (`lyy-nycu/ldap-service`'s
pipeline will pick up the corrected FQDN, exported as
`module.aca.container_app_backend_fqdn`, on the next run). AGW backend health
for the pool confirmed `Healthy` (`Success. Received 200 status code`) after
the change, and remained stable across repeated checks.

## Alternatives Considered

### Keep internal ingress and find some other way to let Application Gateway reach it

Rejected. Per Microsoft's documented ingress model, this is not a
configuration gap to work around — internal ingress is architecturally scoped
to intra-environment calls only. No combination of DNS, VNet peering,
host-header, SNI, or WAF configuration changes the ingress edge's routing
scope. The only way to keep the app internal-only in the ACA sense would be to
insert another intra-environment "front" Container App to receive AGW traffic
and internally call the internal-ingress app — mirroring the
`ca-prevention-be`/`ca-prevention-fe` pattern already used elsewhere in this
same shared environment. Rejected here as disproportionate: it adds a second
compute resource, another hop, and another attack surface purely to preserve
an ingress flag that isn't providing meaningful additional security in this
topology (see Decision).

### Route through a different reverse proxy or expose the ACA app directly

Rejected. Out of scope for this slice's approved topology (Application
Gateway is the mandated on-premises entry point per
`docs/superpowers/plans/active/2026-07-03-slice-10-infrastructure.md`), and
directly exposing ACA ingress was explicitly excluded from the beginning
("Do not expose the hook through public ACA ingress, Front Door, or an
internet-facing reverse proxy in this slice").

### Leave the AGW backend pool Unhealthy and treat the private DNS/host-header fixes as sufficient

Rejected. Those fixes were real and necessary (confirmed by the probe result
changing from `Unknown` to a concrete `404`), but insufficient on their own —
the AGW backend would have stayed permanently unreachable through the
intended path, blocking the entire on-premises portal integration this slice
exists to deliver.

## Follow-up

- `lyy-nycu/ldap-service`'s AGW backend pool member and any cached FQDN
  values must track `module.aca.container_app_backend_fqdn`'s new pattern
  (`<app>.<default-domain>`, no `.internal.` segment) on future re-runs. The
  `PASSWORD_HOOK_BACKEND_FQDN_STAGING` repo variable was updated manually for
  this one-time transition.
- An issue was opened in `lyy-nycu/ldap-service` to record this constraint for
  the shared Application Gateway/ACA environment owners, since any other team
  attempting to front an internal-ingress Container App with this same
  gateway will hit the identical 404.
