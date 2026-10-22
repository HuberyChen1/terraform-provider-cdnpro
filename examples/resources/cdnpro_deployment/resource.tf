# Staging always tracks the newest property version.
resource "cdnpro_deployment" "staging" {
  target = "staging"

  actions = [
    {
      action      = "deploy_property"
      property_id = cdnpro_property.main.id
      version     = cdnpro_property.main.latest_version
    },
    {
      action         = "deploy_cert"
      certificate_id = cdnpro_certificate.prod.id
      version        = cdnpro_certificate.prod.latest_version
    },
  ]
}

# Production is pinned to a version that is bumped by hand once staging has
# been verified, which turns promotion into a separate, human-gated apply.
variable "production_property_version" {
  type        = number
  description = "Property version promoted to production."
}

resource "cdnpro_deployment" "production" {
  target = "production"

  actions = [
    {
      action      = "deploy_property"
      property_id = cdnpro_property.main.id
      version     = var.production_property_version
    },
    {
      action         = "deploy_cert"
      certificate_id = cdnpro_certificate.prod.id
      version        = cdnpro_certificate.prod.latest_version
    },
  ]

  # Never let two deployments race the same environment (DuplicateDeployment).
  depends_on = [cdnpro_deployment.staging]
}
