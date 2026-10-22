resource "cdnpro_secret" "origin_auth" {
  name        = "origin-auth-token"
  description = "Shared secret for the origin authentication header"
  secret      = var.origin_auth_secret # write-only, never stored in state
}

# Edge Logic references the secret by name as plain text ($SECRET(origin-auth-token)),
# which Terraform cannot see, so declare the dependency explicitly.
resource "cdnpro_property" "main" {
  # ...
  depends_on = [cdnpro_secret.origin_auth]
}
