---
page_title: "cdnpro_purge Action - cdnpro"
subcategory: ""
description: |-
  Purges cached content from the CDN. Requires Terraform 1.14 or later.
---

# cdnpro_purge (Action)

Purges cached content from the CDN. Invoke it ad hoc with
`terraform apply -invoke=action.cdnpro_purge.NAME`, or attach it to a resource's
`lifecycle { action_trigger {} }` so a deployment automatically purges affected
paths. Requires Terraform 1.14 or later.

The action waits for the purge to finish (polling at the provider's
`purge_interval`) and fails when the API reports a success rate below 100%.
Nothing is stored in state.

## Example Usage

```terraform
# Purge after every deployment of the property, or invoke by hand:
#   terraform apply -invoke=action.cdnpro_purge.invalidate_main
action "cdnpro_purge" "invalidate_main" {
  config {
    target   = "production"
    dir_urls = ["https://www.example.com/assets/", "https://www.example.com/api/"]
    webhook  = cdnpro_webhook.deploy_notify.id
  }
}

resource "cdnpro_deployment" "production" {
  target = "production"
  actions = [
    {
      action      = "deploy_property"
      property_id = cdnpro_property.main.id
      version     = cdnpro_property.main.latest_version
    },
  ]

  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.cdnpro_purge.invalidate_main]
    }
  }
}
```

## Schema

### Required

- `target` (String) `staging` or `production`.

### Optional

- `action` (String) `invalidate` (default) marks content stale; `delete` removes it. A directory URL or pattern ending in `**` makes the purge recursive.
- `dir_urls` (List of String) Directory URLs to purge.
- `file_headers` (Attributes List) Request headers that are part of the cache key for every entry in `file_urls`. (see [below for nested schema](#nestedatt--file_headers))
- `file_urls` (List of String) File URLs to purge.
- `name` (String) Label for the purge task. At most 128 characters.
- `regex_patterns` (List of String) At most two patterns.
- `webhook` (String) Webhook ID (`cdnpro_webhook.x.id`) notified when the purge completes.

At least one of `file_urls`, `dir_urls` or `regex_patterns` must be set.

<a id="nestedatt--file_headers"></a>
### Nested Schema for `file_headers`

Required:

- `name` (String)
- `value` (String)
