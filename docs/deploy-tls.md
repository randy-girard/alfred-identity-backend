# Deploy TLS (external reverse proxy)

The daemon listens plain HTTP/WS. Terminate TLS in front (Caddy / nginx / Traefik).

## Caddy example

```caddy
sso.example.com {
  reverse_proxy daemon:8080
}
```

GUI sources should use `wss://sso.example.com/ws/sso`.

## nginx example

```nginx
server {
  listen 443 ssl;
  server_name sso.example.com;
  # ssl_certificate …;
  location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
  }
}
```
