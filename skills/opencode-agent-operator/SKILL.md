---
name: opencode-agent-operator
description: Operate, administer, expose, and troubleshoot opencode-agent instances. Use when an agent needs to install, manage, remove, inspect, securely expose, diagnose Tailscale/VPN or internet access, or explain secure defaults for opencode-agent without relying on Codex-specific metadata.
---

# OpenCode Agent Operator

Use this skill to operate `opencode-agent` safely. Treat the repository source and `README.md` as the source of truth; binaries already present in `build/` may be stale and can omit newer commands such as `expose`.

## Core Rules

- Prefer loopback installs: keep OpenCode bound to `127.0.0.1` unless the user explicitly accepts broader exposure.
- Do not print passwords unless the user explicitly asks or a documented workflow requires `--reveal`.
- Treat remote plain HTTP with Basic Auth as unsafe. Use HTTPS termination through Tailscale Serve/Funnel, Cloudflare Tunnel, Caddy, Nginx, or another identity-aware proxy.
- Use Tailscale Serve for private tailnet/VPN access. Use Tailscale Funnel only when the user explicitly requests public internet exposure.
- Use `--dry-run` before risky install or exposure changes when the user wants a preview, or when you need to explain planned files and commands.
- When diagnosing, start with `opencode-agent status <name>` and `opencode-agent expose status <name>` before changing configuration.
- Never create or rely on `agents/openai.yaml`; this skill is portable and agent-neutral.

## Reference Loading

Load only the reference needed for the task:

- Read `references/operations.md` for installation, lifecycle management, passwords, logs, config paths, and removal.
- Read `references/exposure.md` for private VPN/tailnet access, public internet access, HTTPS proxy patterns, and insecure escape hatches.
- Read `references/tailscale-diagnostics.md` for Tailscale Serve/Funnel failures, MagicDNS/DNSName issues, status commands, and health-check interpretation.
- Read `references/security-defaults.md` when explaining secure defaults, warnings, project config seeding, credentials, logging, environment filtering, or service hardening.

## Operating Flow

1. Identify the instance name. Use `default` when the user does not provide one.
2. Inspect current state with `opencode-agent list`, `opencode-agent status <name>`, or their `--json` forms.
3. Choose the smallest safe operation:
   - Install or add an instance: read `references/operations.md`.
   - Expose or remove remote access: read `references/exposure.md`.
   - Diagnose Tailscale or health failures: read `references/tailscale-diagnostics.md`.
   - Explain security posture or warnings: read `references/security-defaults.md`.
4. Run commands with exact argv-style flags from the references. Avoid shell interpolation for secrets.
5. Summarize what changed, the active URL, whether credentials were revealed, and any remaining warnings.

## Common Commands

```bash
opencode-agent install --workdir /path/to/project --port 4096
opencode-agent install --name api --workdir ~/src/api --port 4101
opencode-agent list
opencode-agent status api
opencode-agent logs api
opencode-agent restart api
opencode-agent show-password --reveal api
opencode-agent rotate-password --reveal api
opencode-agent uninstall --purge api
```

For Tailscale private access:

```bash
# Always verify Serve is enabled on the tailnet before exposing.
# A hang on `tailscale serve` means it is not enabled yet.
tailscale serve status   # must return a table, not "Serve is not enabled"

opencode-agent install --workdir /path/to/project --port 4096 --expose tailscale
opencode-agent expose tailscale api --mode serve
opencode-agent expose status api
opencode-agent expose off api
```

For Tailscale public internet access:

```bash
opencode-agent install --workdir /path/to/project --port 4096 --expose tailscale --tailscale-mode funnel --tailscale-public
opencode-agent expose tailscale api --mode funnel --public
```
