resource "cdnpro_webhook" "deploy_notify" {
  name = "deploy-notifications"
  url  = "https://hooks.example.com/cdn-deploy"

  credentials = {
    user       = "cdn-notifier"
    secret_key = var.webhook_secret_key # write-only, never stored in state
  }
}

resource "cdnpro_deployment" "staging" {
  target  = "staging"
  webhook = cdnpro_webhook.deploy_notify.id
  # ...
}
