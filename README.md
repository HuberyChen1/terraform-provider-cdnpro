# Terraform Provider for CDN Pro

`terraform-provider-cdnpro` lets customers manage CDN Pro configuration as code
through the CDN Pro API: properties, certificates, edge hostnames and the
deployments that put them live.

Design reference: Confluence PRD *Terraform Provider for CDN Pro API* (Jira
NCAS-5719). This repository implements all three phases of that PRD.

| Type | Kind | Wraps |
| --- | --- | --- |
| `cdnpro_property` | resource | `/cdn/properties` + versions (folded) |
| `cdnpro_certificate` | resource | `/cdn/certificates` + versions (folded) |
| `cdnpro_edge_hostname` | resource | `/cdn/edgeHostnames` |
| `cdnpro_deployment` | resource | `/cdn/deploymentTasks` + implicit `/cdn/validations` |
| `cdnpro_secret` | resource | `/cdn/secrets` |
| `cdnpro_webhook` | resource | `/cdn/webhooks` |
| `cdnpro_log_config` | resource | `/cdn/report/logConfigs` |
| `cdnpro_purge` | action | `/cdn/purges` |
| `cdnpro_prefetch` | action | `/cdn/prefetches` |
| `cdnpro_hostname` | data source | `/cdn/hostnames/{hostname}` |
| `cdnpro_shields` | data source | `/cdn/shields` |
| `cdnpro_isps` | data source | `/cdn/isps` |
| `cdnpro_client_regions` | data source | `/cdn/clientRegions` |

## Requirements

- Terraform >= 1.11 (write-only attributes are used for certificate material);
  Terraform >= 1.14 for the `cdnpro_purge` / `cdnpro_prefetch` actions.
- Go >= 1.24 to build.

## Usage

```hcl
terraform {
  required_providers {
    cdnpro = {
      source = "HuberyChen1/cdnpro"
    }
  }
}

provider "cdnpro" {
  server   = "https://ngapi.cdnetworks.com" # or CDNPRO_SERVER
  api_user = var.api_user                   # or CDNPRO_API_USER
  api_key  = var.api_key                    # or CDNPRO_API_KEY

  # Optional: tune the fixed-interval polling of async operations.
  polling {
    deployment_interval = "20s"
    deployment_timeout  = "10m"
  }
}
```

See [`examples/`](examples) for a complete property + certificate + edge
hostname + staged deployment configuration, and [`docs/`](docs) for the
attribute reference.

### How the API is hidden

- **Versions are internal.** Changing any configuration attribute of a
  `cdnpro_property` or `cdnpro_certificate` is an in-place update from
  Terraform's point of view. Internally the provider patches the latest
  property version when it has never been deployed, and otherwise creates a new
  version. Certificates always get a new version. Creating a version never
  deploys it.
- **Deployments validate first.** `cdnpro_deployment` runs the property
  validation the API requires before `deploy_property`, retries once on
  `OutdatedSecret`, and gives up with a clear message on
  `DuplicateDeployment`.
- **Adoption without import.** A `cdnpro_deployment` whose actions already match
  the live `versionInProduction` / `versionInStaging` makes no API call on
  create.
- **Optimistic concurrency.** The provider caches `Last-Modified` from every
  read and sends `If-Unmodified-Since` on every PATCH/PUT. A 412 surfaces as
  "Resource modified outside Terraform".
- **Write-only material.** `private_key`, `certificate` and `chain_cert` on
  certificates, `secret` on secrets and `credentials.secret_key` on webhooks are
  never stored in state; SHA-256 hashes in private state drive change
  detection. A changed value shows up in the plan as `last_update_time` (or the
  certificate's `latest_version`) becoming known after apply. The private key
  is AES-128-CBC encrypted with key material derived from the request `Date`
  header, exactly as the API requires.
- **Edge hostnames are polled** on `history[0]`; a successful deletion is
  observed as a `404`.
- **Purge and prefetch are Terraform Actions**, not resources: nothing is
  stored in state. Both wait for the task to finish (purges read their status
  top-level, prefetches under `metaData`) and fail the apply when the API's
  `successRate` is below 100. A prefetch with `start_time` is only submitted,
  not awaited.
- **Data sources are plain pass-throughs** with deterministic sorting; the
  API's sort parameters are deliberately not exposed.

### Auth

Every request is signed as HTTP Basic with the password
`base64(HMAC-SHA1(Date, api_key))`. The key is never logged. `internal/client`
has unit tests pinned to OpenSSL-generated vectors.

## Development

```bash
make build        # go build
make test         # unit tests
make testacc      # acceptance tests against the in-memory mock API (needs the terraform CLI)
make docs         # regenerate docs/ with tfplugindocs
make lint         # gofmt + go vet
```

The acceptance tests spin up an in-memory fake of the CDN Pro API
(`internal/provider/mockapi_test.go`) and drive the provider through the real
Terraform CLI with `terraform-plugin-testing`. They skip when no `terraform`
binary is found; point `TF_ACC_TERRAFORM_PATH` at one to run them.

If `proxy.golang.org` is unreachable:

```bash
export GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn
```

### Local override for manual testing

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "HuberyChen1/cdnpro" = "/path/to/terraform-provider-cdnpro"   # directory containing the built binary
  }
  direct {}
}
```

### Releasing

Releases are cut by [`.github/workflows/release.yml`](.github/workflows/release.yml),
which runs goreleaser on every `v*` tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The workflow cross-compiles for linux/darwin/windows on amd64/arm64, signs the
checksums, attaches `terraform-registry-manifest.json` and leaves a **draft**
GitHub release to review and publish by hand. The Terraform Registry picks the
release up once it is published.

One-off setup, needed before the first release:

1. Generate a GPG key (`gpg --full-generate-key`, RSA 4096, no expiry is
   simplest) and export it: `gpg --armor --export-secret-keys <id>`.
2. Add repository secrets `TERRAFORM_PROVIDER_PRIVATE_KEY` (the armoured private key) and
   `PASSPHRASE`.
3. Upload the public key (`gpg --armor --export <id>`) to
   <https://registry.terraform.io/settings/gpg-keys> for the `HuberyChen1`
   namespace, then publish the provider from the same settings page.

### Layout

```
main.go                       provider entry point
internal/client/              API client: auth, encryption, polling tables, per-resource calls
internal/provider/            provider configuration and the four resources
internal/provider/*_test.go   mock API + acceptance tests
examples/                     HCL examples (also feed tfplugindocs)
docs/                         generated registry docs
```

## Known deviations from the PRD (confirmed against the API source)

- Certificates **do** support `Last-Modified` / `If-Unmodified-Since`; the
  provider uses them.
- `name` is required by the API on deployment tasks and validations; the
  provider generates `terraform-<target>-<timestamp>` when `name` is not set.
- `DuplicateDeployment` is HTTP 409, not 400. Handling is unchanged.
- `auto_renew` is returned by the API as `Off` (capitalised); the provider
  accepts either case and does not report the difference as drift.
- `peer_failure_timeout` cannot be "object or the string `off`" in HCL; it is an
  object with a `disabled` flag that sends the literal `"off"`.
- `redirect_http_to_https` is a string attribute (`true`, `false`, `302`,
  `307`, `308`); HCL booleans convert automatically.
- Webhook metadata uses `lastUpdateTime` (not the PRD's `lastUpdatetime`), and
  the API returns `credentials.secretKey` in plain text on GET; the provider
  still treats it as write-only. The API cannot remove credentials once set, so
  dropping the `credentials` block replaces the webhook.
- The webhook API requires `credentials.user` and `secret_key` together, so both
  are required inside the block.
- The secret PATCH endpoint is a merge patch that only accepts
  `Content-Type: application/json`, which is what the client sends.
- Purge and prefetch status enums also contain `succeeded` and `failed`
  besides `finished`; the poller accepts all three as terminal. Prefetch
  status fields live under `metaData` (capital D), and the not-found code is
  `InvalidPrefetchId`.
- `GET /cdn/hostnames/{hostname}` returns 200 with null fields for an unknown
  hostname; `cdnpro_hostname` exposes this as `found = false` plus a warning.
- Shield `ipV4` / `ipV6` are arrays of CIDRs, exposed as `ipv4` / `ipv6` lists.
- Prefetch accepts an optional `prefetch_level` (`Edge` or `Parent`) that the
  PRD did not list.
