# End-to-end core workflow: certificate, property, edge hostname, staged deployment.

terraform {
  required_version = ">= 1.11"
  required_providers {
    cdnpro = {
      source = "HuberyChen1/cdnpro"
    }
  }
}

provider "cdnpro" {
  server   = var.server
  api_user = var.api_user
  api_key  = var.api_key
}

variable "server" { type = string }
variable "api_user" { type = string }
variable "api_key" {
  type      = string
  sensitive = true
}
variable "private_key_pem" {
  type      = string
  sensitive = true
}
variable "certificate_pem" {
  type      = string
  sensitive = true
}
variable "chain_pem" {
  type      = string
  sensitive = true
  default   = null
}
variable "production_property_version" {
  type    = number
  default = 1
}

resource "cdnpro_certificate" "prod" {
  name        = "example-com-cert"
  auto_renew  = "LE"
  private_key = var.private_key_pem
  certificate = var.certificate_pem
  chain_cert  = var.chain_pem
}

resource "cdnpro_property" "main" {
  name           = "example-site"
  hostnames      = ["www.example.com"]
  syntax_version = 1
  edge_logic     = file("${path.module}/edge-logic/main.dsl")

  origins = [
    {
      name    = "primary"
      servers = ["origin.example.com:80,443"]
    }
  ]

  tls_certificate_id = cdnpro_certificate.prod.id
}

resource "cdnpro_edge_hostname" "main" {
  edge_hostname = "example.edgesuffix.qtlgslb.com"
  client_zones = [
    {
      region = "all"
      isp    = "all"
      action = { type = "deliver", by = ["standard"] }
    }
  ]
}

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

resource "cdnpro_deployment" "production" {
  target     = "production"
  depends_on = [cdnpro_deployment.staging]
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
}

output "edge_hostname" {
  value = cdnpro_edge_hostname.main.edge_hostname
}

output "property_latest_version" {
  value = cdnpro_property.main.latest_version
}
