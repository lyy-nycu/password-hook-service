# Staging Network and Deployment Handoff

**Date:** 2026-07-28  
**Status:** Current. The network ownership model is approved, but its
authoritative repository/state and external-team change details are not yet
established; staging apply has not been executed.

Execute the remaining work through the focused
[Staging Network Readiness and Controlled Apply Plan](../superpowers/plans/active/2026-07-30-staging-network-readiness.md).
This handoff records current evidence; it does not authorize an Azure write.

## Current Deployment Status

- Slice 11 CI/CD, security gates, OIDC authentication, and least-privilege CD
  RBAC are merged to `main`.
- The latest staging CD plan completed successfully.
- `TF_APPLY_MODE` remains `plan`; no staging changes have been applied by CD.
- The accepted plan contains one addition, three in-place changes, and one
  deletion: a Container App image update, provider-normalized Container App
  and Service Bus values, and replacement of the Monitoring Metrics Publisher
  role assignment. No core application resource is planned for deletion.

## Read-Only Preflight Update (2026-07-30)

- VPN connection `s2s-az-juniper-jp-001` is `Connected` with active traffic
  counters. Its Local Network Gateway includes the staging portal network
  `140.113.7.0/24`.
- The hub/workload gateway-transit peerings and the AGW/workload backend
  peerings are all `Connected`.
- `vnet-ag-stg-jpe-001` still has only `agw-to-cae`; no hub-to-AGW peering
  exists. The on-premises routing gap is therefore still present.
- Application Gateway `agw-stg-jpe-001` is running. Private frontend
  `10.0.8.62`, the password-hook HTTPS listener, listener-specific WAF,
  priority-`120` rule, and backend pool are present; the backend reports
  `Healthy`.
- The AGW subnet is `10.0.8.0/26`, has no route table or delegation, and its
  NSG permits required NYCU TCP 443 sources. The password-hook WAF remains the
  narrower `140.113.7.17/32` boundary.
- The deployed password-hook private-endpoint subnet is `10.0.4.96/27` and
  contains the Key Vault, Service Bus, and Managed Redis private endpoints.
- The `lyy-nycu/ldap-service` workflow owns the isolated password-hook
  Application Gateway objects but does not define the required VNet
  peerings. Repository search found no owner definition for those peerings,
  and human activity-log writes do not prove long-term state ownership.

No Azure resource or repository variable was changed during this preflight.
Until the authoritative owner and pipeline for both peering sides are
recorded, do not create peerings out of band and keep `TF_APPLY_MODE=plan`.

## Network Ownership Decision (2026-07-30)

- One dedicated shared-network IaC repository and state will own both the
  hub-to-staging-AGW and staging-AGW-to-hub peering resources.
- `lyy-nycu` is the initial change/rollback operator and has confirmed Azure
  network read/write access.
- `lyy-nycu/ldap-service` continues to manage its existing isolated
  Application Gateway listener/WAF/backend workflow, but will not manage VNet
  peerings. Peering ownership must not depend on the lifecycle of the LDAP
  query service.
- `password-hook-service` consumes the resulting path and does not import the
  shared VNets or peerings into its application Terraform state.

This decision resolves the ownership seam, but it does not complete Task 0.
The exact repository, remote-state key, pipeline identity, environment
approval, technical-team owner, change window, and rollback procedure must be
recorded before any network write.

## Verified Network Topology

| Component | Network placement |
|---|---|
| VPN hub | `vnet-hub-jp-001`, `192.168.10.0/24` |
| Staging Application Gateway | `vnet-ag-stg-jpe-001`, `10.0.8.0/24` |
| Password-hook private frontend | `10.0.8.62`, hostname `api.test.nycu.edu.tw` |
| Shared staging ACA environment | `vnet-stg-jpe-001`, `10.0.4.0/24` |
| ACA delegated subnet | `10.0.4.0/26` |
| ACA environment internal IP | `10.0.4.55` |
| Production ACA VNet | `10.0.3.0/24`; not used by staging |

Application Gateway reaches ACA through the existing direct peering between
the AGW and ACA VNets. This path is healthy and must not be changed. The
password-hook backend currently reports `Healthy`.

The site-to-site VPN is connected, and the staging ACA VNet already uses the
hub gateway through connected gateway-transit peering.

## Outstanding Azure Network Work

The staging AGW VNet does not currently have direct peering with the hub.
Because Azure VNet peering is not transitive, on-premises traffic cannot use
the hub-to-ACA peering as a route to the AGW private frontend.

Before the on-premises test, add dedicated bidirectional peering between the
hub and staging AGW VNets:

- Hub to AGW: allow virtual-network access, forwarded traffic, and gateway
  transit.
- AGW to hub: allow virtual-network access and forwarded traffic, and use the
  hub's remote gateway.
- Keep the existing AGW-to-ACA peering unchanged and do not enable remote
  gateway use on that peering.

Confirm the infrastructure/state owner before making this change so the
peering is not created out of band from another repository or pipeline.

After the change, verify that both new peerings are connected, the VPN remains
connected, and the password-hook AGW backend remains healthy.

## On-Premises / Juniper Handoff

The network team must configure the VPN path for destination `10.0.8.0/24`
and confirm TCP 443 reachability to `10.0.8.62`.

The originating portal address must remain `140.113.7.17`; source NAT to
another address will be rejected by the WAF policy. The Azure NSG permits
NYCU source ranges on TCP 443, while the password-hook WAF policy permits
only `140.113.7.17/32`.

On-premises split-horizon DNS must resolve `api.test.nycu.edu.tw` to
`10.0.8.62`. Testing must preserve that hostname for TLS and SNI validation.

Direct access to the ACA environment IP is not the acceptance path because it
bypasses Application Gateway, WAF, and the trusted-proxy contract.

## Recommended Execution Order

1. Capture the current VPN, peering, AGW listener, WAF, and backend-health
   baseline.
2. Add the hub-to-staging-AGW bidirectional peering through its approved
   infrastructure owner.
3. Confirm Azure-side routing and verify no regression to VPN or AGW-to-ACA
   health.
4. Hand the destination prefix, private frontend, hostname, source-address,
   and TLS requirements to the Juniper/network team.
5. Complete the on-premises DNS, VPN selector, route, and no-SNAT changes.
6. From the approved portal host, verify DNS, TCP 443, TLS/SNI, health, and one
   redacted signed request returning `202 Accepted`.
7. Only after the network path is ready, perform the controlled staging apply
   and return `TF_APPLY_MODE` to `plan` after verification.
