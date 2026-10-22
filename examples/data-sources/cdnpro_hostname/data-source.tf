data "cdnpro_hostname" "check" {
  hostname = "www.example.com"
}

output "production_property_version" {
  value = data.cdnpro_hostname.check.found ? data.cdnpro_hostname.check.property_in_production.version : null
}
