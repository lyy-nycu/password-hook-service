# Staging Network and Deployment Handoff

**Date:** 2026-07-28  
**Status:** Azure network preparation pending; staging apply not yet executed.

## Current Deployment Status

- Slice 11 CI/CD, security gates, OIDC authentication, and least-privilege CD
  RBAC are merged to `main`.
- The latest staging CD plan completed successfully.
- `TF_APPLY_MODE` remains `plan`; no staging changes have been applied by CD.
- The accepted plan contains one addition, three in-place changes, and one
  deletion: a Container App image update, provider-normalized Container App
  and Service Bus values, and replacement of the Monitoring Metrics Publisher
  role assignment. No core application resource is planned for deletion.

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

