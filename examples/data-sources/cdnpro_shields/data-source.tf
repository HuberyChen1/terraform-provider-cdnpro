data "cdnpro_shields" "all" {}

# Resolve a shield by region into the ID that origins[].shield expects.
locals {
  us_shield_id = [for s in data.cdnpro_shields.all.shields : s.id if s.region == "us"][0]
}

resource "cdnpro_property" "main" {
  # ...
  origins = [
    {
      name    = "primary"
      servers = ["origin.example.com:80,443"]
      shield  = local.us_shield_id
    }
  ]
}

# The shield's egress ranges are what the origin firewall must allow.
output "us_shield_allowlist" {
  value = [for s in data.cdnpro_shields.all.shields : concat(s.ipv4, s.ipv6) if s.region == "us"][0]
}
