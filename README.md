# terraform-provider-beenfar

Terraform (and OpenTofu) provider for the [beenfar](https://github.com/jacobalberty/beenfar)
network controller. It manages networks, wifi networks, RADIUS servers,
switch ports and device adoption.

Resources manage only the attributes set in their configuration. The others
are neither changed nor reported as drift, so a resource can be managed
partly by Terraform and partly by the beenfar UI: for instance Terraform owns
a wifi network's security settings while the UI chooses which access points
broadcast it. See [docs/guides/partial-management.md](docs/guides/partial-management.md)
and [examples/partial-management](examples/partial-management/main.tf).

## Usage

```hcl
terraform {
  required_providers {
    beenfar = { source = "jacobalberty/beenfar" }
  }
}

provider "beenfar" {
  url   = "https://beenfar.example:8080" # or BEENFAR_URL
  token = var.beenfar_token              # or BEENFAR_TOKEN
}
```

Create a token with `beenfard admin token -username admin -name terraform`.

| Resource | |
|---|---|
| `beenfar_network` | VLAN and subnet |
| `beenfar_wifi_network` | SSID, security, network, access point groups |
| `beenfar_wifi_network_ap_groups` | only the access point groups of a wifi network managed elsewhere |
| `beenfar_radius_server` | RADIUS server for WPA-enterprise (write-only secret) |
| `beenfar_switch_port` | settings of one port of an adopted switch |
| `beenfar_device` | adopts a device (destroy forgets it) |

Data sources: `beenfar_pending_devices`, `beenfar_adapters`, `beenfar_device`
(by id or MAC), `beenfar_network` and `beenfar_radius_server` (by id or name).
Every resource imports by id; `beenfar_device` also by MAC address. The full
reference is in [docs/](docs/).

**Tracing.** Every call to beenfar carries a W3C `traceparent`, so the
controller's spans for one Terraform operation share a trace. Spans are
exported over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` (or
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) is set, and continue the trace in
`TRACEPARENT` when a CI system or wrapper sets it. `OTEL_SDK_DISABLED=true`
turns tracing off.

## Development

The provider talks to beenfar through the generated Connect clients of the
`github.com/jacobalberty/beenfar/api` module, required at a released
`api/vX.Y.Z` version (or, before a release, the pseudo-version of a pushed
beenfar commit); there is no `replace` directive.

```sh
go test ./...                     # unit tests
go generate ./...                 # goverter conversions (internal/conv/gen) and docs/ (tfplugindocs)
```

Acceptance tests run against a real `beenfard` on a temporary SQLite
database, built from the beenfar commit the api module version points at:

```sh
BEENFARD=$(.github/scripts/build-beenfard.sh /tmp) TF_ACC=1 go test ./internal/provider/
# with OpenTofu instead of Terraform:
TF_ACC_TERRAFORM_PATH=$(command -v tofu) TF_ACC_PROVIDER_HOST=registry.opentofu.org \
  BEENFARD=/tmp/beenfard TF_ACC=1 go test ./internal/provider/
```

Commits follow [Conventional Commits](https://www.conventionalcommits.org/);
releases are tagged automatically from them (`.github/workflows/release.yml`).
Publishing signed binaries to the registries is not set up yet.
