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
