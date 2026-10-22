data "cdnpro_isps" "us" {
  region_code = "na.us"
}

# Name-to-code resolution stays an explicit HCL expression.
locals {
  att_isp_code = [for i in data.cdnpro_isps.us.isps : i.code if i.name == "ATT"][0]
}
