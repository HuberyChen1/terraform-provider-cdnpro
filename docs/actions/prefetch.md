---
page_title: "cdnpro_prefetch Action - cdnpro"
subcategory: ""
description: |-
  Warms the production cache with the given URLs. Requires Terraform 1.14 or later.
---

# cdnpro_prefetch (Action)

Warms the production cache with the given URLs. Prefetches are not scoped to an
environment: they always target production. Invoke ad hoc with
`terraform apply -invoke=action.cdnpro_prefetch.NAME` or from a
`lifecycle { action_trigger {} }`. Requires Terraform 1.14 or later.

The action waits for completion (polling at the provider's `purge_interval`)
and fails when the success rate is below 100%. A prefetch with `start_time`
is only submitted, not awaited. Nothing is stored in state.

## Example Usage

```terraform
# Warm the production cache. Prefetches are not scoped to staging/production.
action "cdnpro_prefetch" "warm_cache" {
  config {
    file_list = [
      { url = "https://www.example.com/assets/app.js" },
      {
        url     = "https://www.example.com/assets/app.css"
        headers = [{ name = "Accept-Encoding", value = "br" }]
      },
    ]
    regions = ["Asia", "Europe"]
    # start_time = "2026-10-23T02:00:00Z"  # optional; must be within the next 24 hours
  }
}

resource "cdnpro_deployment" "production" {
  # ...
  lifecycle {
    action_trigger {
      events  = [after_update]
      actions = [action.cdnpro_prefetch.warm_cache]
    }
  }
}
```

## Schema

### Required

- `file_list` (Attributes List) URLs to prefetch. (see [below for nested schema](#nestedatt--file_list))

### Optional

- `name` (String) Label for the prefetch task. At most 1000 characters.
- `prefetch_level` (String) `Parent` (default) warms the parent cache layer; `Edge` warms edge servers as well.
- `regions` (Set of String) Continents to prefetch into: `Africa`, `Antarctica`, `Asia`, `Europe`, `North America`, `Oceania`, `South America`, `Mainland China`. Default: all.
- `start_time` (String) Schedule the prefetch instead of running it now, formatted `2026-10-22T08:00:00Z`. Must be within the next 24 hours at apply time.
- `webhook` (String) Webhook ID notified when the prefetch completes.

<a id="nestedatt--file_list"></a>
### Nested Schema for `file_list`

Required:

- `url` (String) At most 2048 characters.

Optional:

- `headers` (Attributes List) Request headers that vary the cache key for this URL only. Each entry has a required `name` and `value`.
