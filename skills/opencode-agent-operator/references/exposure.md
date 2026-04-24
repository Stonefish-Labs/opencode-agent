# Exposure Reference

Use this reference when exposing `opencode-agent` instances to a private VPN/tailnet or to the public internet.

## Exposure Principles

- OpenCode itself serves HTTP and uses Basic Auth through `OPENCODE_SERVER_USERNAME` and `OPENCODE_SERVER_PASSWORD`.
- Basic Auth over plain remote HTTP is unsafe, even on a VPN or tailnet.
- Prefer loopback HTTP between OpenCode and a local HTTPS proxy or tunnel.
- Keep OpenCode bound to `127.0.0.1` wherever possible.
- For remote access, advertise an HTTPS URL.
- Public internet exposure requires explicit user intent and stronger controls such as identity-aware proxying, mTLS, SSO, access policies, rate limiting, and logs.

## Tailscale Serve: Private Tailnet/VPN Access

Use Tailscale Serve when the user wants private tailnet access. It keeps OpenCode on loopback and exposes HTTPS inside the tailnet:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --port 4096 \
  --expose tailscale
```

The agent plans and applies a command equivalent to:

```bash
tailscale serve --bg --https=443 --yes http://127.0.0.1:4096
```

It advertises:

```text
https://<machine>.<tailnet>.ts.net
```

Add or change Tailscale Serve exposure after install:

```bash
opencode-agent expose tailscale api --mode serve
```

Preview the change:

```bash
opencode-agent expose tailscale api --mode serve --dry-run
```

Expose under a path:

```bash
opencode-agent expose tailscale api --mode serve --path /opencode
```

Use a different HTTPS listener:

```bash
opencode-agent expose tailscale api --mode serve --https-port 8443
```

Show exposure status:

```bash
opencode-agent expose status api
opencode-agent expose status api --json
```

Disable exposure:

```bash
opencode-agent expose off api
```

Preview disabling exposure:

```bash
opencode-agent expose off api --dry-run
```

When exposure is turned off, the agent clears the exposure config and resets the advertised URL to loopback HTTP for the instance port.

## Tailscale Funnel: Public Internet Access

Use Tailscale Funnel only when the user explicitly asks for public internet exposure. Funnel exposes the service to the public internet through Tailscale and must be treated as high risk.

Install with Funnel:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --port 4096 \
  --expose tailscale \
  --tailscale-mode funnel \
  --tailscale-public
```

Enable Funnel after install:

```bash
opencode-agent expose tailscale api --mode funnel --public
```

Funnel requires explicit public confirmation:

- Install flag: `--tailscale-public`
- Expose flag: `--public`

Funnel HTTPS ports are limited to:

```text
443
8443
10000
```

Example with a supported alternate port:

```bash
opencode-agent expose tailscale api --mode funnel --public --https-port 8443
```

The agent plans and applies a command equivalent to:

```bash
tailscale funnel --bg --https=443 --yes http://127.0.0.1:4096
```

For public exposure, also recommend rotating credentials, checking project-config warnings, and applying Tailscale policy controls before sharing the URL.

## HTTPS Proxy Or Tunnel Patterns

Use this pattern when the user already has Cloudflare Tunnel, Caddy, Nginx, a load balancer, or an identity-aware proxy.

Install locally and keep OpenCode on loopback:

```bash
opencode-agent install --workdir /path/to/project --port 4096
```

Cloudflare Tunnel quick example:

```bash
cloudflared tunnel --url http://127.0.0.1:4096
```

When a stable HTTPS endpoint exists, install with an advertised URL:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --port 4096 \
  --advertise-url https://opencode.example.com
```

The advertised URL should be what remote clients use. The proxy should forward to:

```text
http://127.0.0.1:<port>
```

For shared or public deployments, prefer an identity-aware proxy, mTLS, SSO, access policies, and rate limiting. Basic Auth is shared per instance and does not provide per-client identity.

## Unsafe Escape Hatches

Use these only for trusted lab scenarios or when the user explicitly accepts the risk.

Allow a non-loopback HTTP advertised URL:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --bind-host 100.64.1.2 \
  --advertise-host 100.64.1.2 \
  --allow-insecure-remote-http
```

Allow fallback from a Tailscale IPv4 bind address to all interfaces:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --bind-host 100.64.1.2 \
  --advertise-host 100.64.1.2 \
  --allow-insecure-remote-http \
  --allow-all-interfaces-fallback
```

All-interface fallback may broaden exposure to LAN, Wi-Fi, VM, container, or public interfaces. Prefer fail-closed loopback plus HTTPS proxying instead.

## Exposure Command Reference

Install-time Tailscale flags:

```text
--expose tailscale
--tailscale-mode serve|funnel
--tailscale-public
--tailscale-https-port 443
--tailscale-path /
--tailscale-yes=true
```

Post-install exposure flags:

```text
opencode-agent expose tailscale [name] [--mode serve|funnel] [--public]
opencode-agent expose tailscale [name] [--https-port PORT] [--path PATH]
opencode-agent expose tailscale [name] [--yes=true] [--dry-run] [--restart=true]
opencode-agent expose status [name] [--json]
opencode-agent expose off [name] [--yes=true] [--dry-run]
```

Rules enforced by the agent:

- `--expose tailscale` requires OpenCode to bind to `127.0.0.1`.
- `--advertise-host` and `--advertise-url` cannot be used with `--expose tailscale`.
- Funnel requires `public=true`.
- Funnel HTTPS port must be `443`, `8443`, or `10000`.
- Exposure path is normalized to start with `/`; spaces, tabs, newlines, `?`, and `#` are rejected.

