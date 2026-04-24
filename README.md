# OpenCode Agent

`opencode-agent` is a small cross-platform supervisor for running named
OpenCode HTTP servers on your own machines. It is built for home labs and
Tailnet access: install one or more named agents, keep them alive at login, and
inspect them with a Docker-like CLI.

## Features

- Named local instances with `default` as the easy path.
- Per-instance OS services:
  - macOS LaunchAgent: `com.opencode.agent.<name>`
  - Linux user systemd unit: `opencode-agent-<name>.service`
  - Windows Scheduled Task: `OpenCodeAgent-<name>`
- Generated Basic Auth passwords stored only in the OS keychain.
- Tailscale-friendly networking:
  - detects `100.64.0.0/10` Tailnet IPv4 addresses
  - advertises the Tailnet URL
  - tries binding OpenCode to the Tailnet IP, then falls back to `0.0.0.0`
- Separate process, local health, and Tailnet health reporting.

## Install An Agent

```bash
opencode-agent install --workdir /path/to/project --port 4096
```

The install command prints the generated password once:

```text
Installed default.
URL: http://100.x.y.z:4096
Username: opencode
Password: ...
```

That password is stored in the OS keychain service `opencode-agent`, scoped to
the instance name. Config files never contain passwords.

For multiple project servers on one machine, use names:

```bash
opencode-agent install --name api --workdir ~/src/api --port 4101
opencode-agent install --name web --workdir ~/src/web --port 4102
```

## Manage Instances

```bash
opencode-agent list
opencode-agent status api
opencode-agent logs api
opencode-agent restart api
opencode-agent show-password api
opencode-agent rotate-password api
opencode-agent uninstall api --purge
```

`list` prints local instances only:

```text
NAME     STATUS      PID    URL                    WORKDIR       LOCAL  TAILNET  SERVICE
default  running     12345  http://100.x.y.z:4096  /home/me/app  ok     ok       installed
api      local-only   2345  http://100.x.y.z:4101  /home/me/api  ok     fail     installed
```

JSON is available for scripts:

```bash
opencode-agent list --json
opencode-agent status api --json
```

## Commands

- `install [--name default] --workdir DIR [--port 4096]`
- `list` or `ps`
- `status [name]`
- `start [name]`
- `stop [name]`
- `restart [name]`
- `logs [name] [--lines 120]`
- `show-password [name]`
- `rotate-password [name]`
- `uninstall [name] [--purge]`

## Config And State

Config and state are per user.

- macOS/Linux config: `~/.config/opencode-agent/instances/<name>.json`
- macOS/Linux state: `~/.local/state/opencode-agent/instances/<name>/`
- Windows config: `%APPDATA%\opencode-agent\instances\<name>.json`
- Windows state: `%LOCALAPPDATA%\opencode-agent\instances\<name>\`

The installed service entrypoint is `opencode-agent run --name <name>`.

## Development

```bash
go test ./...
go vet ./...
./scripts/build.sh
```

Build artifacts are written to `build/`.

## Security Model

This project is intended for trusted LAN/Tailscale-style home lab use. It serves
OpenCode over HTTP with strong generated Basic Auth credentials. Do not expose
the advertised URL directly to the public internet.
