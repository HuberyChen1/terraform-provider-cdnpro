resource "cdnpro_edge_hostname" "main" {
  edge_hostname = "example.edgesuffix.qtlgslb.com"
  description   = "Edge hostname for example-site"

  client_zones = [
    {
      region = "all"
      isp    = "all"
      weight = 100
      action = {
        type        = "deliver"
        by          = ["standard"]
        enable_ipv6 = true
      }
    }
  ]

  deliver_by_https = {
    state = "on"
    alpn  = "h2,h3"
  }
}
