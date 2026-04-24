# OpenCode Agent

`opencode-agent` is a small cross-platform supervisor for running named
OpenCode servers on your own machines. It installs per-user services, stores
generated Basic Auth credentials in the OS keychain, and keeps OpenCode bound to
loopback by default so remote access can be added through an HTTPS proxy or
tunnel.

## Features

- Named local instances with `default` as the easy path.
- Per-instance OS services:
  - macOS LaunchAgent: `com.opencode.agent.<name>`
  - Linux user systemd unit: `opencode-agent-<name>.service`
  - Windows Scheduled Task: `OpenCodeAgent-<name>`
- Generated Basic Auth passwords stored in the OS keychain.
- Secure default networking: bind and advertise `127.0.0.1`.
- Explicit remote access through `--advertise-url https://...` or managed
  Tailscale Serve/Funnel exposure.
- Secure project `opencode.json` seeding plus project-config audit warnings.
- Local/remote health reporting with unsafe remote HTTP checks skipped by default.
- Redacted, rotated logs with restrictive file permissions.

## Install An Agent

```bash
opencode-agent install --workdir /path/to/project --port 4096
```

Default installs bind OpenCode to `127.0.0.1` and create
`<workdir>/opencode.json` when it is missing:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "permission": {
    "*": "ask",
    "read": "allow",
    "grep": "allow",
    "glob": "allow",
    "list": "allow",
    "external_directory": "ask"
  }
}
```

Existing OpenCode project configs are preserved and audited. Warnings are shown
for dangerous auto-allows, local MCP commands, non-HTTPS remote MCP URLs, and
credential-looking MCP config keys.

The install command does not print passwords unless you explicitly ask:

```bash
opencode-agent install --workdir /path/to/project --reveal
```

Without `--reveal`, the password is stored in the OS keychain service
`opencode-agent`, scoped to the instance name. Config files never contain the
server password.

For multiple project servers on one machine, use names:

```bash
opencode-agent install --name api --workdir ~/src/api --port 4101
opencode-agent install --name web --workdir ~/src/web --port 4102
```

## Remote Access

OpenCode currently serves HTTP and uses Basic Auth through
`OPENCODE_SERVER_USERNAME` and `OPENCODE_SERVER_PASSWORD`. Basic Auth over plain
HTTP is not safe for remote access, even on a VPN or Tailnet. Use loopback HTTP
between OpenCode and a local proxy, then expose HTTPS to clients.

Tailscale Serve example for private tailnet access:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --port 4096 \
  --expose tailscale
```

This keeps OpenCode bound to `127.0.0.1`, configures
`tailscale serve --bg --https=443 --yes http://127.0.0.1:4096`, and advertises
the node's `https://<machine>.<tailnet>.ts.net` URL.

Tailscale Funnel is public internet exposure and requires an explicit public
confirmation:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --port 4096 \
  --expose tailscale \
  --tailscale-mode funnel \
  --tailscale-public
```

Manage exposure after install:

```bash
opencode-agent expose tailscale api --mode serve
opencode-agent expose status api
opencode-agent expose off api
```

Use `--tailscale-path` during install or `--path` during `expose tailscale` to
mount under a URL path such as `/opencode`. Use `--tailscale-https-port` or
`--https-port` to choose the HTTPS listener; Funnel supports `443`, `8443`, and
`10000`.

Cloudflare Tunnel example:

```bash
opencode-agent install --workdir /path/to/project --port 4096
cloudflared tunnel --url http://127.0.0.1:4096
```

Caddy/Nginx/custom HTTPS proxy pattern:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --port 4096 \
  --advertise-url https://opencode.example.com
```

Use an identity-aware proxy, mTLS, SSO, access policies, and rate limiting for
public or shared deployments. Keep OpenCode itself bound to loopback where
possible.

Explicit escape hatches are available for trusted lab scenarios:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --bind-host 100.64.1.2 \
  --advertise-host 100.64.1.2 \
  --allow-insecure-remote-http
```

Tailnet bind failure no longer falls back to all interfaces. To restore that
old behavior intentionally:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --bind-host 100.64.1.2 \
  --advertise-host 100.64.1.2 \
  --allow-insecure-remote-http \
  --allow-all-interfaces-fallback
```

## Manage Instances

```bash
opencode-agent list
opencode-agent status api
opencode-agent logs api
opencode-agent restart api
opencode-agent show-password api
opencode-agent rotate-password api --reveal
opencode-agent uninstall api --purge
```

`list` prints local instances only:

```text
NAME     STATUS      PID    URL                         WORKDIR       LOCAL  TAILNET  WARN  SERVICE
default  running     12345  http://127.0.0.1:4096       /home/me/app  ok     ok       1     installed
api      local-only   2345  https://opencode.example    /home/me/api  ok     fail     2     installed
```

Use `status` for full warning details. JSON is available for scripts:

```bash
opencode-agent list --json
opencode-agent status api --json
```

## Commands

- `install [--name default] --workdir DIR [--port 4096]`
- `install --advertise-url https://host.example [--bind-host 127.0.0.1]`
- `install --expose tailscale [--tailscale-mode serve|funnel]`
- `install --no-project-config-seed`
- `install --allow-env NAME [--environment-policy filtered|minimal|inherit]`
- `list` or `ps`
- `status [name]`
- `expose tailscale [name] [--mode serve|funnel] [--public]`
- `expose status [name] [--json]`
- `expose off [name]`
- `start [name]`
- `stop [name]`
- `restart [name]`
- `logs [name] [--lines 120]`
- `show-password [name]`
- `rotate-password [name] [--restart=false] [--reveal]`
- `uninstall [name] [--purge]`

## Config And State

Config and state are per user.

- macOS/Linux config: `~/.config/opencode-agent/instances/<name>.json`
- macOS/Linux state: `~/.local/state/opencode-agent/instances/<name>/`
- Windows config: `%APPDATA%\opencode-agent\instances\<name>.json`
- Windows state: `%LOCALAPPDATA%\opencode-agent\instances\<name>\`

The installed service entrypoint is `opencode-agent run --name <name>`.

Logs live under the instance state directory as `agent.log` plus rotated files
`agent.log.1` through `agent.log.5`. They are stored with `0600` permissions and
redact common secret shapes, but logs can still contain sensitive operational
context such as project paths and tool output. Review logs before attaching them
to issues or support requests.

## Development

```bash
go test ./...
go vet ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.23.0 ./...
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
./scripts/build.sh
```

Build artifacts are written to `build/`. Release builds include raw binaries
and per-platform packages:

- `opencode-agent-darwin-arm64.tar.gz`
- `opencode-agent-darwin-amd64.tar.gz`
- `opencode-agent-linux-amd64.tar.gz`
- `opencode-agent-linux-arm64.tar.gz`
- `opencode-agent-windows-amd64.zip`

Each package contains the platform binary, `README.md`, `LICENSE`, and bundled
operator skills from `skills/`.

## Release Verification

Download the release assets, then verify checksums:

```bash
shasum -a 256 -c SHA256SUMS
```

Verify the signed checksum file:

```bash
cosign verify-blob \
  --certificate SHA256SUMS.pem \
  --signature SHA256SUMS.sig \
  SHA256SUMS
```

Each binary, package, and SBOM is also signed:

```bash
cosign verify-blob \
  --certificate opencode-agent-linux-amd64.pem \
  --signature opencode-agent-linux-amd64.sig \
  opencode-agent-linux-amd64
```

Verify GitHub artifact provenance:

```bash
gh attestation verify opencode-agent-linux-amd64 \
  --repo Stonefish-Labs/opencode-agent
```

## Security Model

`opencode-agent` supervises OpenCode, which can read files, edit files, run
commands, and call external services as your OS user. OpenCode permissions,
Basic Auth, VPNs, and reverse proxies reduce risk; they are not a sandbox.

For higher-risk deployments, run the service in a dedicated OS account,
container, VM, or remote workspace with least-privilege filesystem access and
strong network controls.
