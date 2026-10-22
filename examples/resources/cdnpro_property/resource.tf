resource "cdnpro_property" "main" {
  name        = "example-site"
  description = "Main production property"

  hostnames      = ["www.example.com", "example.com"]
  edge_logic     = file("${path.module}/edge-logic/main.dsl")
  syntax_version = 1

  origins = [
    {
      name               = "primary"
      servers            = ["origin.example.com:80,443"]
      supported_protocol = "https"
      verify_origin      = true
      host_header        = "origin.example.com"
      direct_connection  = "noDirect"
    }
  ]

  tls_certificate_id     = cdnpro_certificate.prod.id
  tls_min_version        = "1.2"
  tls_max_version        = "1.3"
  redirect_http_to_https = true
}
