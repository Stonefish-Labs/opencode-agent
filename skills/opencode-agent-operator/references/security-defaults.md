# Security Defaults Reference

Use this reference to explain or verify `opencode-agent` secure defaults, warnings, and risk tradeoffs.

## Network Defaults

Default install binds and advertises loopback:

```text
Bind host:      127.0.0.1
Advertise URL:  http://127.0.0.1:<port>
```

Loopback is the safe default because OpenCode serves HTTP and uses shared Basic Auth. Remote access should be added through HTTPS proxying or Tailscale Serve/Funnel.

The CLI refuses non-loopback remote HTTP advertised URLs unless the user explicitly passes:

```text
--allow-insecure-remote-http
```

All-interface binds are warned on. Fallback to `0.0.0.0` from a Tailscale IPv4 bind is disabled unless the user explicitly passes:

```text
--allow-all-interfaces-fallback
```

## Authentication And Credentials

OpenCode uses one shared Basic Auth credential per instance. The supervisor injects:

```text
OPENCODE_SERVER_USERNAME
OPENCODE_SERVER_PASSWORD
```

Defaults:

- Username defaults to `opencode`.
- Password is generated with cryptographic randomness when omitted.
- Password is stored in the OS keychain service `opencode-agent`, account `instance:<name>`.
- Password is not written to the agent config.
- Password is not printed unless `--reveal` is used.

Security implications:

- Basic Auth is reusable and shared, so it does not provide per-client identity.
- Basic Auth over remote HTTP is unsafe.
- Public or shared deployments should use identity-aware HTTPS proxying, mTLS, SSO, access policies, rate limiting, and audit logs.

Rotate credentials when exposure changes, after debugging sessions where credentials were revealed, or after suspected compromise:

```bash
opencode-agent rotate-password --reveal <name>
```

## Project Config Seeding

On install, if `<workdir>/opencode.json` is missing, the agent seeds a restrictive OpenCode project config:

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

This seed uses `0600` file permissions. Existing project configs are preserved and audited rather than overwritten.

Use this escape hatch only when the user intentionally wants no seed:

```text
--no-project-config-seed
```

## Project Config Warnings

The agent audits these files:

```text
<workdir>/opencode.json
<workdir>/opencode.jsonc
<workdir>/.opencode/opencode.json
<workdir>/.opencode/opencode.jsonc
```

Warnings include:

- `project_config.permission.allow_all`: all OpenCode permissions are set to allow.
- `project_config.permission.allow_wildcard`: wildcard permission `*` is set to allow.
- `project_config.permission.dangerous_allow`: security-sensitive tools are auto-allowed.
- `project_config.tools.dangerous_enable`: legacy tools config enables security-sensitive tools.
- `project_config.mcp.local`: local MCP commands run unsandboxed as the user.
- `project_config.mcp.local_network_tool`: local MCP command uses tools such as `curl`, `wget`, `nc`, or `ncat`.
- `project_config.mcp.remote_invalid_url`: remote MCP URL is missing or invalid.
- `project_config.mcp.remote_insecure_url`: remote MCP URL is not HTTPS.
- `project_config.mcp.credential_key`: MCP config contains credential-looking keys.
- `project_config.read_error` or `project_config.parse_error`: the config could not be inspected.

Security-sensitive permission keys include:

```text
bash
edit
write
task
agent
skill
webfetch
websearch
mcp
```

When warnings appear, report them plainly and recommend tightening the project config before exposing the instance remotely.

## Agent Status Warnings

`opencode-agent status <name>` can emit these warning codes:

- `auth.shared_basic`: one shared Basic Auth credential is used per instance.
- `auth.password_missing`: no Basic Auth password was found in the OS keychain.
- `network.insecure_remote_http`: non-loopback HTTP is advertised without explicit opt-in.
- `network.all_interfaces_bind`: service is configured to bind all interfaces.
- `network.all_interfaces_fallback`: all-interface fallback is enabled.
- `network.public_tailscale_funnel`: Tailscale Funnel exposes the agent to the public internet.

Treat warnings as operational risk signals. Some are expected, such as `auth.shared_basic`, but should still influence deployment guidance.

## Environment Filtering

The supervisor builds a child environment for `opencode serve`.

Policies:

- `filtered` is the default.
- `minimal` keeps only essential variables plus explicitly allowed variables.
- `inherit` passes through most parent variables, except existing `OPENCODE_SERVER_*` entries.

Add explicit environment variables with repeated `--allow-env` flags:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --allow-env OPENAI_API_KEY \
  --allow-env ANTHROPIC_API_KEY
```

Use `inherit` only when the user accepts the broader environment exposure:

```bash
opencode-agent install \
  --workdir /path/to/project \
  --environment-policy inherit
```

The supervisor always sets:

```text
OPENCODE_SERVER_USERNAME
OPENCODE_SERVER_PASSWORD
OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=true
```

## Logs

Logs live under the instance state directory:

```text
agent.log
agent.log.1
agent.log.2
agent.log.3
agent.log.4
agent.log.5
```

The supervisor rotates logs around 10 MiB and keeps five rotated files. Logs are written under private state directories and redact:

- Exact stored secret values known to the supervisor.
- Authorization, Cookie, and Set-Cookie header values.
- Bearer tokens.
- Credential-looking key/value pairs.
- URL userinfo.
- Control characters.

Do not assume logs are safe to publish. They may still include project paths, prompts, command output, and other sensitive operational context.

## Service Hardening

macOS uses a per-user LaunchAgent with `RunAtLoad` and `KeepAlive`.

Linux uses a per-user systemd service with hardening settings including:

```text
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=full
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
ProtectKernelLogs=true
ProtectClock=true
LockPersonality=true
RestrictRealtime=true
RestrictSUIDSGID=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native
CapabilityBoundingSet=
AmbientCapabilities=
```

Windows uses a per-user Scheduled Task with limited run level:

```text
schtasks /Create /TN OpenCodeAgent-<name> /SC ONLOGON /RL LIMITED /F
```

These service defaults reduce accidental exposure but do not sandbox OpenCode from the installing user's files. Treat OpenCode permissions and project configs as important defense-in-depth, not as a hard security boundary.

## Secure Review Checklist

Before exposing an instance beyond loopback:

1. Run `opencode-agent status <name>` and review warnings.
2. Confirm project config does not auto-allow dangerous tools.
3. Confirm the advertised URL is HTTPS.
4. Prefer Tailscale Serve or an identity-aware HTTPS proxy.
5. Avoid `--allow-insecure-remote-http` and `--allow-all-interfaces-fallback`.
6. Rotate credentials if the URL changes from local-only to remote.
7. Review logs before sharing them.
