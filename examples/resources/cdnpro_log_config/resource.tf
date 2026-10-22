resource "cdnpro_log_config" "main" {
  description = "Standard access logs for example-site"
  hostnames   = ["www.example.com", "example.com"]

  log_download_storage_days      = 30
  log_download_file_span_minutes = 60
}
