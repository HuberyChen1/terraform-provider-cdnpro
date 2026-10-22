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
