# Gateway CLI Management

## Purpose

The `acpctl` CLI SHALL support gateway resources as a first-class resource type across `get`, `delete`, and a dedicated `gateway` subcommand tree. This provides operators with full CLI access to inspect gateway connection details and configure local openshell CLI access against provisioned gateways.

**Related:** `gateway-provisioning.spec.md` — gateway resource model and reconciliation; `openshell-sandbox.spec.md` — sandbox execution via gateways

---

## Requirements

### Requirement: Get Gateways

The `acpctl get` command SHALL support `gateways` as a resource type with aliases `gateway` and `gw`.

When listing all gateways (`acpctl get gateways`), the output SHALL display a table with the following columns:

| Column | Width | Content |
|--------|-------|---------|
| NAME | 24 | `gateway.name` |
| IMAGE | 50 | `gateway.image` |
| DNS NAMES | 50 | Comma-separated `gateway.serverDnsNames` |
| AGE | 10 | Relative time since `created_at` |

When retrieving a single gateway by name or ID (`acpctl get gateway <name>`), the output SHALL display the table row followed by a connection info block.

The command SHALL support JSON output via the standard `--output json` flag.

#### Scenario: List all gateways

- GIVEN gateways "alpha" and "beta" exist
- WHEN the user runs `acpctl get gateways`
- THEN a table renders with NAME, IMAGE, DNS NAMES, and AGE columns
- AND both gateways appear as rows

#### Scenario: Get a single gateway by name

- GIVEN gateway "alpha" exists in project "platform"
- WHEN the user runs `acpctl get gateway alpha`
- THEN the table renders with one row for "alpha"
- AND a connection info block is printed below the table

#### Scenario: Gateway not found

- GIVEN no gateway named "nonexistent" exists
- WHEN the user runs `acpctl get gateway nonexistent`
- THEN the command exits with an error: `gateway "nonexistent" not found`

#### Scenario: JSON output

- GIVEN gateway "alpha" exists
- WHEN the user runs `acpctl get gateway alpha -o json`
- THEN the gateway object is printed as JSON

### Requirement: Gateway Connection Info

When a single gateway is retrieved, the CLI SHALL print a connection info block after the table containing:

- **Cluster DNS**: The in-cluster service address (`openshell-gateway.<namespace>.svc.cluster.local:8080`)
- **Server SANs**: The gateway's configured DNS names (only if `serverDnsNames` is non-empty)
- **TLS Secret**: The name and namespace of the mTLS secret (`openshell-server-tls`)
- **Setup hint**: The `acpctl gateway setup-cli <name>` command to configure local CLI access

The namespace SHALL be derived from the gateway's `projectID`, lowercased.

#### Scenario: Connection info with DNS names

- GIVEN gateway "alpha" has `serverDnsNames: ["gw.example.com"]` and belongs to project "PLATFORM"
- WHEN the user runs `acpctl get gateway alpha`
- THEN the connection info shows:
  - Cluster DNS: `openshell-gateway.platform.svc.cluster.local:8080`
  - Server SANs: `gw.example.com`
  - TLS Secret: `openshell-server-tls (namespace: platform)`
  - Setup hint: `acpctl gateway setup-cli alpha`

#### Scenario: Connection info without DNS names

- GIVEN gateway "beta" has an empty `serverDnsNames` list
- WHEN the user runs `acpctl get gateway beta`
- THEN the Server SANs line is omitted from the connection info

### Requirement: Delete Gateway

The `acpctl delete` command SHALL support `gateway` as a resource type with aliases `gateways` and `gw`.

#### Scenario: Delete a gateway

- GIVEN gateway "alpha" exists
- WHEN the user runs `acpctl delete gateway alpha`
- THEN the gateway is deleted via the API
- AND the output shows `gateway/alpha deleted`

#### Scenario: Delete nonexistent gateway

- GIVEN no gateway named "nonexistent" exists
- WHEN the user runs `acpctl delete gateway nonexistent`
- THEN the command exits with an error

### Requirement: Gateway Subcommand Tree

The `acpctl gateway` top-level command SHALL provide a subcommand tree for gateway management operations. Running `acpctl gateway` without a subcommand SHALL display help text.

### Requirement: Gateway Setup CLI

The `acpctl gateway setup-cli <name>` command SHALL configure local openshell CLI access for a named gateway. The command performs the following steps in order:

1. **Resolve gateway** — Fetch the gateway by name or ID from the API server
2. **Prerequisite checks** — Verify `kubectl` and `openshell` are in PATH; verify the target namespace exists in the cluster; verify the `openshell-server-tls` secret exists
3. **Extract mTLS certificates** — Extract `ca.crt`, `tls.crt`, and `tls.key` from the `openshell-server-tls` secret and write them to `~/.config/openshell/gateways/<name>/mtls/`
4. **Start port-forward** — Run `kubectl port-forward` to the `openshell-gateway` StatefulSet on port 8080 with an ephemeral local port
5. **Register gateway** — Remove any existing openshell gateway registration with the same name, then register the gateway via `openshell gateway add --name <name> --local https://localhost:<port>`
6. **Verify connectivity** — Run `openshell -g <name> provider list` to verify the connection works
7. **Hold port-forward** — Keep the port-forward running in the foreground until the user presses Ctrl+C

The namespace SHALL be derived from the gateway's `projectID`, lowercased.

Private key files (`tls.key`) SHALL be written with `0600` permissions. Other certificate files SHALL use `0644`.

#### Scenario: Successful setup

- GIVEN gateway "alpha" exists in project "platform"
- AND kubectl is configured with access to namespace "platform"
- AND the `openshell-server-tls` secret exists in namespace "platform"
- AND `kubectl` and `openshell` are installed
- WHEN the user runs `acpctl gateway setup-cli alpha`
- THEN mTLS certs are extracted to `~/.config/openshell/gateways/alpha/mtls/`
- AND a port-forward starts to `statefulset/openshell-gateway` in namespace "platform"
- AND the gateway is registered with the openshell CLI
- AND a connectivity check runs
- AND the port-forward remains active until interrupted

#### Scenario: kubectl not installed

- GIVEN `kubectl` is not in PATH
- WHEN the user runs `acpctl gateway setup-cli alpha`
- THEN the command exits with error: `kubectl not found in PATH: required for gateway setup`

#### Scenario: openshell not installed

- GIVEN `openshell` is not in PATH
- WHEN the user runs `acpctl gateway setup-cli alpha`
- THEN the command exits with error: `openshell not found in PATH: required for gateway setup`

#### Scenario: Namespace does not exist

- GIVEN gateway "alpha" belongs to project "missing"
- AND namespace "missing" does not exist in the cluster
- WHEN the user runs `acpctl gateway setup-cli alpha`
- THEN the command exits with error: `namespace "missing" does not exist in the cluster`

#### Scenario: TLS secret not found

- GIVEN gateway "alpha" belongs to project "platform"
- AND the `openshell-server-tls` secret does not exist in namespace "platform"
- WHEN the user runs `acpctl gateway setup-cli alpha`
- THEN the command exits with error indicating the secret is not found and the gateway may not be fully provisioned

#### Scenario: Connectivity check fails

- GIVEN all setup steps succeed
- BUT `openshell provider list` fails
- WHEN the user runs `acpctl gateway setup-cli alpha`
- THEN a warning is printed with troubleshooting guidance
- AND the port-forward remains active (the command does not exit on verification failure)

### Requirement: API URL Short Flag

The `acpctl` root command SHALL support `-U` as a short flag alias for `--api-url`, allowing `acpctl -U https://api.example.com get gateways`.

#### Scenario: Short flag for API URL

- GIVEN a valid API server at `https://api.example.com`
- WHEN the user runs `acpctl -U https://api.example.com get gateways`
- THEN the command uses `https://api.example.com` as the API server URL
