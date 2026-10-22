# Changelog

## 0.1.0 (2026-10-22)

Phase 3 (operational actions and data sources) of the CDN Pro Terraform provider (NCAS-5719):

- `cdnpro_purge` and `cdnpro_prefetch` as Terraform Actions (Terraform >= 1.14):
  invokable ad hoc or from `lifecycle.action_trigger`, polled to completion,
  failing on a partial `successRate`.
- Data sources `cdnpro_hostname`, `cdnpro_shields`, `cdnpro_isps` and
  `cdnpro_client_regions` with deterministic ordering.
- Provider `polling` block gains `purge_interval` / `purge_timeout`.
- Mock API endpoints and acceptance tests for all of the above.

Phase 2 (supporting configuration) of the CDN Pro Terraform provider (NCAS-5719):

- `cdnpro_secret`: write-only `secret` tracked by hash and rotated in place.
- `cdnpro_webhook`: write-only `credentials.secret_key`; removing the
  `credentials` block replaces the webhook because the API cannot clear it.
- `cdnpro_log_config`: `/cdn/report/logConfigs` with the API's integer ID
  carried as a string and server defaults mirrored in the schema.
- Mock API and acceptance coverage for all three.

Phase 1 (core) of the CDN Pro Terraform provider (NCAS-5719):

- Provider configuration with HMAC-SHA1 request signing, environment variable
  fallbacks and per-endpoint-group polling overrides.
- `cdnpro_property`: property + folded versions, in-place patch vs new version
  depending on the frozen flag, `Last-Modified` / `If-Unmodified-Since`.
- `cdnpro_certificate`: certificate + folded versions, write-only PEM material
  with AES-128-CBC private key encryption and hash-based change detection.
- `cdnpro_edge_hostname`: async provisioning polled on `history[0]`, 404 as the
  deletion success signal, opt-in `force_delete`.
- `cdnpro_deployment`: implicit validation before `deploy_property`, no-op
  adoption, drift detection per action, inverse `remove_*` on destroy,
  `OutdatedSecret` retry, `DuplicateDeployment` guidance.
- In-memory mock of the CDN Pro API and acceptance tests driven through the
  Terraform CLI.
