location / {
    origin_pass primary;
    proxy_cache_valid 200 10m;
}
