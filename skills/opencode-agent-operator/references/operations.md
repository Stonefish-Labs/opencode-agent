# Operations Reference

Use this reference for installing, managing, inspecting, rotating credentials for, and removing `opencode-agent` instances.

## Source Of Truth

Prefer the current repository source and `README.md` over prebuilt binaries in `build/`. The source currently defines commands in `internal/cli/cli.go`, service behavior in `internal/service/service.go`, instance paths in `internal/instance/instance.go`, and health output in `internal/health/health.go`.

## Concepts

- An instance is a named supervised OpenCode server. The default name is `default`.
- Each instance has its own config, state directory, log files, OS service entry, Basic Auth credential, port, bind host, and advertised URL.
- The installed service entrypoint is:

```bash
opencode-agent run --name <name>
```

- Default service names:
  - macOS LaunchAgent: `com.opencode.agent.<name>`
  - Linux user systemd unit: `opencode-agent-<name>.service`
  - Windows Scheduled Task: `OpenCodeAgent-<name>`

## Install A Local Instance

Default install:

```bash
opencode-agent install --workdir /path/to/project --port 4096
```

This creates or updates a per-user service, stores credentials in the OS keychain, copies the `opencode-agent` executable into the state root, and starts OpenCode with Basic Auth environment variables.

Named instances let one machine host multiple project servers:

```bash
opencode-agent install --name api --workdir ~/src/api --port 4101
opencode-agent install --name web --workdir ~/src/web --port 4102
```

Use `--dry-run` to preview config, service files, project-config actions, exposure commands, and install commands without writing:

```bash
opencode-agent install --name api --workdir ~/src/api --port 4101 --dry-run
```

Useful install flags:

```text
--name default
--workdir DIR
--port 4096
--binary PATH
--username opencode
--password VALUE
--advertise-url https://host.example
--advertise-host HOST
--bind-host HOST
--expose tailscale
--tailscale-mode serve|funnel
--tailscale-public
--tailscale-https-port 443
--tailscale-path /
--tailscale-yes=true
--allow-insecure-remote-http
--allow-all-interfaces-fallback
--environment-policy filtered|minimal|inherit
--allow-env NAME
--no-project-config-seed
--reveal
--dry-run
```

For lifecycle commands with an optional instance name, flags may be placed before or after the name. For example, both `opencode-agent status --json api` and `opencode-agent status api --json` are valid.

Do not use `--advertise-host` or `--advertise-url` together with `--expose tailscale`; the CLI rejects that combination. Do not use `--advertise-host` and `--advertise-url` together; choose one.

## Passwords

By default, install generates a Basic Auth password and stores it in the OS keychain under service `opencode-agent`, account `instance:<name>`. The agent config never stores the password.

Install without printing the password:

```bash
opencode-agent install --workdir /path/to/project --port 4096
```

Install and print the generated or provided password only when explicitly needed:

```bash
opencode-agent install --workdir /path/to/project --port 4096 --reveal
```

Show credentials for an existing instance:

```bash
opencode-agent show-password api
```

`show-password` prints the URL, username, and password. Use `status` when you only need instance metadata without revealing credentials.

Rotate a password and restart by default:

```bash
opencode-agent rotate-password --reveal api
```

Rotate without restarting:

```bash
opencode-agent rotate-password --restart=false --reveal api
```

When reporting results, avoid including passwords unless the user asked for them.

## Manage Instances

List local instances:

```bash
opencode-agent list
opencode-agent ps
opencode-agent list --json
```

Inspect one instance:

```bash
opencode-agent status api
opencode-agent status --json api
opencode-agent status --name api --json
```

Start, stop, or restart:

```bash
opencode-agent start api
opencode-agent stop api
opencode-agent restart api
```

Read logs:

```bash
opencode-agent logs api
opencode-agent logs --lines 300 api
```

Uninstall the service but keep config, state, and keychain credential:

```bash
opencode-agent uninstall api
```

Uninstall and remove config, state, and keychain password:

```bash
opencode-agent uninstall --purge api
```

If no instance configs remain after uninstall, the shared installed executable under the state root is removed.

## Config, State, And Logs

macOS/Linux paths:

```text
Config: ~/.config/opencode-agent/instances/<name>.json
State:  ~/.local/state/opencode-agent/instances/<name>/
Log:    ~/.local/state/opencode-agent/instances/<name>/agent.log
Binary: ~/.local/state/opencode-agent/bin/opencode-agent
```

Windows paths:

```text
Config: %APPDATA%\opencode-agent\instances\<name>.json
State:  %LOCALAPPDATA%\opencode-agent\instances\<name>\
Log:    %LOCALAPPDATA%\opencode-agent\instances\<name>\agent.log
Binary: %LOCALAPPDATA%\opencode-agent\bin\opencode-agent.exe
```

Environment overrides:

```text
OPENCODE_AGENT_CONFIG_DIR
OPENCODE_AGENT_STATE_DIR
```

Logs rotate as `agent.log` plus `agent.log.1` through `agent.log.5`. Log directories are created with restrictive permissions, and log files are intended to be private to the user. Logs redact common secret patterns but can still contain project paths, tool output, and operational context.

## Status Interpretation

`opencode-agent list` prints:

```text
NAME STATUS PID URL WORKDIR LOCAL TAILNET WARN SERVICE
```

Status words:

- `running`: process is alive and advertised/tailnet health is OK.
- `local-only`: process is alive and local health is OK, but advertised/tailnet health is not OK.
- `unhealthy`: process is alive, but local health did not pass.
- `stopped`: no live process is recorded.

`opencode-agent status <name>` prints URL, exposure, workdir, auth mode, service state, process state, local health, tailnet health, last exit/error, and warnings.

## Project Config During Install

By default, install creates `<workdir>/opencode.json` if missing with ask-by-default permissions:

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

Existing OpenCode project configs are preserved and audited. Use `--no-project-config-seed` only when the user intentionally wants to skip secure seeding.
