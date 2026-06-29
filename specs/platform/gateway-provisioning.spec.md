# Gateway Provisioning Specification

**Date:** 2026-06-26  
**Status:** Design  
**Related:** `openshell-sandbox-provisioning.spec.md` — gateway mode usage; `control-plane.spec.md` — CP reconciliation patterns  
**Skill:** `skills/build/full-stack-pipeline/` — wave-based implementation pipeline

---

## Purpose

The control plane SHALL provision and reconcile OpenShell gateway deployments in project namespaces. This replaces the assumption in `openshell-sandbox-provisioning.spec.md` (Iteration 1) that gateways are pre-installed. The control plane discovers which project namespaces to manage via a platform configuration ConfigMap and applies gateway resource manifests using Kubernetes APIs.

Gateway provisioning is an **infrastructure-layer capability**. Gateway state is NOT stored in PostgreSQL; the platform-config ConfigMap is the source of truth for which namespaces require gateways.

This enables:
- **Centralized gateway lifecycle management** — ACP team controls gateway versions
- **Deterministic deployments** — Gateway manifests versioned with ACP code
- **Self-service namespace provisioning** — Add namespace to ConfigMap → gateway auto-deploys
- **Consistent gateway configuration** — All tenants use the same vetted gateway configuration

---

## Requirements

### Requirement: Platform Configuration Discovery

The control plane SHALL read gateway configuration from a ConfigMap named `platform-config` in its own namespace. This ConfigMap SHALL contain a list of namespaces that the control plane is responsible for managing.

#### Scenario: Load platform configuration on startup

- GIVEN ACP starts up
- AND a ConfigMap named `platform-config` exists in the ACP namespace
- AND the ConfigMap contains a `namespaces` key with a YAML list
- WHEN ACP loads configuration
- THEN ACP SHALL parse the namespace list
- AND ACP SHALL store the list of project namespaces in memory

#### Scenario: Platform ConfigMap not found

- GIVEN ACP starts up
- AND the ConfigMap `platform-config` does NOT exist in the ACP namespace
- WHEN ACP attempts to load configuration
- THEN ACP SHALL log an error "platform-config ConfigMap not found"
- AND ACP SHALL NOT proceed with gateway deployments
- AND ACP SHALL retry loading the ConfigMap periodically

#### Scenario: Platform ConfigMap malformed

- GIVEN the `platform-config` ConfigMap exists
- AND the `namespaces` key contains invalid YAML
- WHEN ACP attempts to parse the ConfigMap
- THEN ACP SHALL log an error with parse failure details
- AND ACP SHALL NOT proceed with gateway deployments
- AND ACP SHALL wait for the ConfigMap to be corrected

---

### Requirement: Namespace Validation

For each namespace listed in the platform configuration, the control plane SHALL verify that the namespace exists in the cluster before attempting to deploy a gateway.

#### Scenario: All namespaces exist

- GIVEN `platform-config` lists namespaces: `tenant-alpha`, `tenant-beta`, `tenant-gamma`
- AND all three namespaces exist in the cluster
- WHEN ACP validates namespaces
- THEN ACP SHALL proceed with gateway reconciliation for all three namespaces

#### Scenario: Namespace does not exist

- GIVEN `platform-config` lists namespace `tenant-nonexistent`
- AND this namespace does NOT exist in the cluster
- WHEN ACP validates namespaces
- THEN ACP SHALL log an error "namespace tenant-nonexistent not found"
- AND ACP SHALL skip gateway deployment for that namespace
- AND ACP SHALL continue processing other valid namespaces
- AND ACP SHALL NOT crash or enter a crash loop

#### Scenario: Namespace deleted at runtime

- GIVEN ACP is managing `tenant-alpha` with a deployed gateway
- AND `platform-config` still lists `tenant-alpha`
- WHEN an admin deletes the namespace from the cluster
- THEN ACP SHALL detect the missing namespace in the next reconcile cycle
- AND ACP SHALL log a warning "namespace tenant-alpha listed in config but not found in cluster"
- AND ACP SHALL skip that namespace until it reappears or is removed from the ConfigMap

---

### Requirement: Gateway Manifest Loading

The control plane SHALL load gateway resource manifests from its container filesystem. Gateway manifests SHALL be stored in the ACP codebase and packaged into the ACP container image.

#### Scenario: Load gateway manifests from filesystem

- GIVEN ACP container includes gateway manifests at `/manifests/gateway/`
- AND the directory contains files: `deployment.yaml`, `service.yaml`, `serviceaccount.yaml`, `rbac.yaml`
- WHEN ACP loads gateway manifests
- THEN ACP SHALL read all YAML files from the manifests directory
- AND ACP SHALL parse each file into Kubernetes resource objects

#### Scenario: Required manifest file missing

- GIVEN ACP container is expected to have `/manifests/gateway/deployment.yaml`
- AND this file does NOT exist
- WHEN ACP attempts to load manifests
- THEN ACP SHALL log an error "required manifest file not found: /manifests/gateway/deployment.yaml"
- AND ACP SHALL NOT proceed with gateway deployments
- AND ACP SHALL exit with a non-zero code

---

### Requirement: Gateway Deployment

For each project namespace, the control plane SHALL deploy an OpenShell gateway by applying the loaded manifests to the namespace. The gateway SHALL consist of a Deployment, Service, ServiceAccount, and RBAC resources.

All gateway resources SHALL use fixed names:
- Deployment: `openshell-gateway`
- Service: `openshell-gateway`
- ServiceAccount: `openshell-gateway`
- Role: `openshell-gateway`
- RoleBinding: `openshell-gateway`

All gateway resources SHALL carry the following labels:
- `app.kubernetes.io/name=openshell`
- `app.kubernetes.io/component=gateway`
- `app.kubernetes.io/managed-by=agent-control-plane`

The gateway Deployment SHALL specify:
- **SecurityContext:** `runAsNonRoot: true`, `allowPrivilegeEscalation: false`, capabilities `drop: [ALL]`
- **Resource requests:** `cpu: 100m`, `memory: 256Mi`
- **Resource limits:** `cpu: 500m`, `memory: 512Mi`

Gateway resources SHALL NOT have OwnerReferences (consistent with current session pod pattern where resources are managed via labels, not ownership chains).

#### Scenario: Deploy gateway to empty namespace

- GIVEN `tenant-alpha` namespace exists
- AND no OpenShell gateway is deployed in `tenant-alpha`
- WHEN ACP reconciles gateways
- THEN ACP SHALL apply all gateway manifests to `tenant-alpha`
- AND each resource SHALL have its namespace field set to `tenant-alpha`
- AND the gateway Deployment SHALL be created
- AND the gateway Service SHALL be created with label `app.kubernetes.io/name=openshell`
- AND the ServiceAccount and RBAC resources SHALL be created

#### Scenario: Gateway already exists (idempotency)

- GIVEN `tenant-alpha` has an OpenShell gateway already deployed
- AND `platform-config` lists `tenant-alpha`
- WHEN ACP reconciles gateways
- THEN ACP SHALL detect the existing gateway via Service label `app.kubernetes.io/name=openshell`
- AND ACP SHALL apply the latest gateway configurations using client-go Server-Side Apply (SSA) or equivalent
- AND ACP SHALL NOT create duplicate resources
- AND ACP MAY verify the gateway is healthy

#### Scenario: Multiple namespaces, parallel deployment

- GIVEN `platform-config` lists 10 namespaces without gateways
- WHEN ACP reconciles gateways on startup
- THEN ACP SHALL deploy gateways to all 10 namespaces
- AND deployments MAY execute in parallel
- AND failure in one namespace SHALL NOT block deployments in other namespaces

---

### Requirement: Gateway Discovery

The control plane SHALL discover existing gateways in a namespace by looking for a Service with the label `app.kubernetes.io/name=openshell`.

#### Scenario: Gateway detected via Service label

- GIVEN `tenant-alpha` has a Service with label `app.kubernetes.io/name=openshell`
- WHEN ACP checks for an existing gateway
- THEN ACP SHALL consider the gateway as already deployed
- AND ACP SHALL NOT attempt to create a new gateway

#### Scenario: Service exists but Deployment missing

- GIVEN `tenant-alpha` has a Service with label `app.kubernetes.io/name=openshell`
- AND the corresponding Deployment does NOT exist
- WHEN ACP reconciles gateways
- THEN ACP SHALL detect the inconsistent state
- AND ACP SHALL create the missing Deployment
- AND ACP SHALL log a warning "inconsistent state detected: Service exists without Deployment"

---

### Requirement: Gateway Version Updates

When the gateway manifests in the ACP codebase are updated, the control plane SHOULD detect drift between deployed gateway resources and the new manifests, and update the deployed resources accordingly using client-go Server-Side Apply (SSA) or similar.

If drift detection is not implemented, the control plane SHALL follow a create-only pattern (existing gateway resources are not modified; manual deletion required to trigger recreation with new manifests). This matches the current behavior for session pods.

#### Scenario: Detect drift after ACP upgrade (if drift detection implemented)

- GIVEN `tenant-alpha` has gateway v0.0.70 deployed
- AND ACP is upgraded with manifests specifying gateway v0.0.71
- WHEN ACP reconciles gateways after the upgrade
- THEN ACP SHALL detect that the deployed Deployment differs from the manifest
- AND ACP SHALL update the Deployment to v0.0.71
- AND the update SHALL be a rolling update (zero downtime)

**Note:** Drift detection MAY use template hash annotations, image tag comparison, resource spec comparison, or other mechanisms. The create-only pattern is acceptable for initial iterations.

---

### Requirement: Dynamic Configuration Updates

The control plane SHALL detect changes to the platform-config ConfigMap and reconcile gateway state accordingly.

When a new namespace is added to the platform configuration, the control plane SHALL deploy a gateway to the new namespace.

**Note:** Implementation MAY use Kubernetes ConfigMap watch (recommended) or periodic polling to detect configuration changes.

#### Scenario: New namespace added to ConfigMap

- GIVEN ACP is managing `tenant-alpha` and `tenant-beta`
- AND an admin updates `platform-config` to add `tenant-gamma`
- WHEN ACP detects the ConfigMap change
- THEN ACP SHALL validate that `tenant-gamma` namespace exists
- AND ACP SHALL deploy a gateway to `tenant-gamma`
- AND the existing gateways in `tenant-alpha` and `tenant-beta` SHALL NOT be modified

---

### Requirement: Gateway Deployment Failure Handling

When gateway deployment fails (e.g., ImagePullBackOff, insufficient permissions), the control plane SHALL log the error and retry on subsequent reconcile cycles without crashing.

#### Scenario: Image pull failure

- GIVEN ACP attempts to deploy a gateway to `tenant-alpha`
- AND the gateway manifest specifies an image that does not exist
- WHEN Kubernetes attempts to pull the image
- THEN the Deployment SHALL enter ImagePullBackOff state
- AND ACP SHALL log an error with the namespace and failure reason
- AND ACP SHALL retry on the next reconcile cycle
- AND ACP SHALL NOT mark the namespace as permanently failed

#### Scenario: Insufficient RBAC permissions

- GIVEN ACP ServiceAccount does NOT have permission to create Deployments in `tenant-alpha`
- WHEN ACP attempts to apply gateway manifests
- THEN the Kubernetes API SHALL return a Forbidden error
- AND ACP SHALL log an error "insufficient permissions to create Deployment in namespace tenant-alpha"
- AND ACP SHALL continue processing other namespaces where it has permissions

---

### Requirement: Platform ConfigMap Default Values

When a namespace in the platform configuration has no additional configuration specified, the control plane SHALL apply the gateway with default values.

#### Scenario: Namespace with no additional configuration

- GIVEN `platform-config` contains:
  ```yaml
  namespaces:
    - name: tenant-alpha
  ```
- WHEN ACP processes the ConfigMap
- THEN ACP SHALL deploy the gateway to `tenant-alpha` using default manifest values
- AND no namespace-specific customization SHALL be applied

---

### Requirement: Separation from Agent Configuration

Gateway provisioning SHALL be independent of agent definitions. Agent-specific configuration (schedules, prompts, policies) is out of scope for this specification.

**Note:** Future work may introduce per-tenant agent ConfigMaps that ACP discovers, but the schema and discovery mechanism are not defined in this specification.

---

## Migration

### Relationship to Existing Specs

This specification supersedes the "Gateway provisioning" constraint in `openshell-sandbox-provisioning.spec.md` (line 20-22), which stated:

> "Gateway provisioning — the OpenShell gateway is assumed to already be deployed in each project namespace; ACP will not create it. A future iteration should have the control plane provision and reconcile gateway lifecycle per project namespace..."

This specification IS that future iteration.

### Backward Compatibility

This is a new capability. When disabled (no `platform-config` ConfigMap), ACP behavior is unchanged — it will not attempt to manage gateways.

When enabled, this specification does NOT conflict with:
- Existing gateway mode (`OPENSHELL_USE_GATEWAY=true`) — that controls whether sessions use the gateway for sandboxing
- File mode (`OPENSHELL_ENABLED=true`) — unaffected
- Direct pod mode (`OPENSHELL_USE_GATEWAY=false`, `OPENSHELL_ENABLED=false`) — unaffected

### Existing Consumers

| Consumer | Impact |
|---|---|
| `kube_reconciler.go` | No changes — gateway provisioning is a separate reconciler |
| `openshell/gateway_client.go` | No changes — continues to use gateways for sandbox creation |
| `pod_sync.go` | No changes |
| `StandardNamespaceProvisioner` | No changes — continues to create/verify namespaces as before |

**Note:** Namespaces MAY be managed by ACP (current behavior) or externally via App Interface. Gateway provisioning works with both models — it only requires that namespaces exist before gateway deployment.

---

## Configuration

### Environment Variables

No new environment variables are required. Gateway provisioning is enabled by the presence OPENSHELL_USE_GATEWAY=true

### ConfigMap Schema

**Name:** `platform-config`  
**Namespace:** Same namespace where ACP is deployed (e.g., `ambient-code`)

**Required Keys:**
- `namespaces` (string, YAML format) — List of namespace objects

**Example:**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: platform-config
  namespace: ambient-code
data:
  namespaces: |
    - name: tenant-alpha
    - name: tenant-beta
    - name: tenant-gamma
```

**Future extensibility:** Additional namespace-specific configuration may be added as additional fields under each namespace entry.

---

## RBAC Requirements

The ACP ServiceAccount SHALL have sufficient permissions to:
- Get and watch ConfigMaps in its own namespace (to load and detect changes to `platform-config`)
- List and get namespaces (to validate namespace existence)
- Create, update, patch, and get Deployments, Services, ServiceAccounts, Roles, and RoleBindings in project namespaces

**Note:** The exact ClusterRole definition will be determined during implementation. The control plane follows the principle of least privilege — permissions are scoped to only what is necessary for gateway provisioning.

---

## Template Packaging

Gateway manifests SHALL be:
- Stored in the ACP codebase at `components/ambient-control-plane/manifests/gateway/`
- Generated once during development using `helm template` (NOT Helm at runtime)
- Packaged into the ACP container image at build time
- Read from the container filesystem at `/manifests/gateway/` at runtime

**Manifest files:**
- `deployment.yaml` — Gateway Deployment
- `service.yaml` — Gateway Service
- `serviceaccount.yaml` — ServiceAccount for gateway pods
- `rbac.yaml` — Role/RoleBinding for gateway ServiceAccount

---

## Future Work

The following capabilities are deferred to future iterations:

1. **Namespace-specific gateway configuration:** Different namespaces MAY require different gateway settings (e.g., resource limits, image overrides). This can be added via additional fields in the platform-config namespace entries.

2. **Advanced drift detection:** The initial implementation MAY use a create-only pattern (matching current session pod behavior). Future iterations can add hash-based drift detection or spec comparison for automatic updates.

---

## References

- [OpenShell Gateway Helm Chart](https://github.com/NVIDIA/OpenShell/tree/main/deploy/helm/openshell)
- [openshell-sandbox-provisioning.spec.md](./openshell-sandbox-provisioning.spec.md) — Gateway usage for sandboxing
- [control-plane.spec.md](./control-plane.spec.md) — Control plane architecture
- Design document: `gateway-provisioning-design-es.md`
