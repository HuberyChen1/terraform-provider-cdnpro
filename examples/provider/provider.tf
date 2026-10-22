terraform {
  required_providers {
    cdnpro = {
      source = "HuberyChen1/cdnpro"
    }
  }
}

provider "cdnpro" {
  server   = "https://ngapi.cdnetworks.com" # or set CDNPRO_SERVER
  api_user = var.cdnpro_api_user            # or set CDNPRO_API_USER
  api_key  = var.cdnpro_api_key             # or set CDNPRO_API_KEY

  # Optional. Async operations are polled at a fixed interval until they finish.
  polling {
    validation_interval    = "10s"
    validation_timeout     = "10m"
    deployment_interval    = "20s"
    deployment_timeout     = "10m"
    edge_hostname_interval = "20s"
    edge_hostname_timeout  = "10m"
  }
}

variable "cdnpro_api_user" {
  type = string
}

variable "cdnpro_api_key" {
  type      = string
  sensitive = true
}
