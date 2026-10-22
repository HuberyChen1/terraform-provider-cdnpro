data "cdnpro_client_regions" "all" {}
data "cdnpro_isps" "all" {}

locals {
  us_region_code = [for r in data.cdnpro_client_regions.all.regions : r.code if r.name == "United States"][0]
  att_isp_code   = [for i in data.cdnpro_isps.all.isps : i.code if i.name == "ATT"][0]
}

resource "cdnpro_edge_hostname" "main" {
  edge_hostname = "example.edgesuffix.qtlgslb.com"

  client_zones = [
    {
      region = local.us_region_code
      isp    = local.att_isp_code
      action = { type = "deliver", by = ["premium"] }
    },
    {
      region = "all"
      isp    = "all"
      action = { type = "deliver", by = ["standard"] }
    },
  ]
}
