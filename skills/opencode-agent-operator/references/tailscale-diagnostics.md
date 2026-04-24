# Tailscale Diagnostics Reference

Use this reference when Tailscale Serve/Funnel exposure fails, the advertised URL is wrong, health checks fail, or the user cannot reach an instance over the tailnet or public internet.

## First Commands

Start with agent state:

```bash
opencode-agent status <name>
opencode-agent expose status <name>
opencode-agent logs --lines 200 <name>
```

Use JSON when scripting or when details are hard to read:

```bash
opencode-agent status --json <name>
opencode-agent expose status <name> --json
```

Then inspect Tailscale directly:

```bash
tailscale status --json
tailscale serve status
tailscale funnel status
```

## Pre-Flight: Verify Tailscale Serve/Funnel Is Enabled

**Always run this before attempting any Tailscale Serve or Funnel exposure.** Serve must be explicitly enabled on the tailnet in the admin console; it is not on by default. If it is not enabled, `tailscale serve` will hang waiting for user interaction rather than returning an error.

```bash
tailscale serve status
```

If the output contains:

```text
Serve is not enabled on your tailnet.
To enable, visit: https://login.tailscale.com/f/serve?node=...
```

Direct the user to open that URL and enable Serve before continuing. Do not attempt `opencode-agent expose tailscale` or `tailscale serve` until this is confirmed. Similarly for Funnel:

```bash
tailscale funnel status
```

Only proceed with exposure once `tailscale serve status` returns a serve table (even an empty one) without the "not enabled" message.

## How opencode-agent Resolves Tailscale

For Tailscale exposure, the agent runs:

```bash
tailscale status --json
```

It expects:

- `BackendState` to be `Running`.
- `Self` to exist.
- `Self.DNSName` to be present.

If `BackendState` is not `Running`, fix the local Tailscale client first. If `Self.DNSName` is missing, enable or repair MagicDNS/HTTPS certificate support for Tailscale Serve.

Common failure messages and likely causes:

```text
tailscale is not running (state: <state>)
```

Tailscale is stopped, logged out, blocked, or not fully connected.

```text
tailscale status did not include this node
```

The local Tailscale daemon did not report `Self`; authenticate or repair the client.

```text
tailscale status did not include a DNS name; enable MagicDNS/HTTPS certificates for Serve
```

MagicDNS, DNS name publication, or HTTPS certificate support is unavailable for this node/tailnet.

## Serve And Funnel Commands

For Serve mode, opencode-agent applies:

```bash
tailscale serve --bg --https=<port> --yes http://127.0.0.1:<opencode-port>
```

For Funnel mode, it applies:

```bash
tailscale funnel --bg --https=<port> --yes http://127.0.0.1:<opencode-port>
```

When a non-root path is configured, the command includes:

```text
--set-path=/path
```

Disable exposure:

```bash
tailscale serve --https=<port> --yes off
tailscale funnel --https=<port> --yes off
```

Prefer using the agent wrapper when the instance config should be updated:

```bash
opencode-agent expose off <name>
```

## Diagnostic Checklist

1. Confirm the instance is installed and alive:

```bash
opencode-agent status <name>
```

Look for `Service`, `Process`, `Local health`, `Tailnet health`, `Last exit`, and `Last error`.

2. Confirm local health:

```bash
curl -i http://127.0.0.1:<port>/global/health
```

The endpoint may require Basic Auth. The agent status command uses stored credentials for health checks.

3. Confirm Tailscale state:

```bash
tailscale status --json
```

Check `BackendState`, `Self.DNSName`, and the node name.

4. Confirm Serve or Funnel state:

```bash
opencode-agent expose status <name>
tailscale serve status
tailscale funnel status
```

Match the served target to:

```text
http://127.0.0.1:<opencode-port>
```

5. Confirm path and HTTPS port:

- Default path `/` maps to the root URL.
- A custom path such as `/opencode` should make the URL `https://<host>/opencode`.
- Serve supports the selected HTTPS port accepted by Tailscale.
- Funnel supports only `443`, `8443`, and `10000`.

6. Confirm credentials:

```bash
opencode-agent show-password <name>
```

Use this only when the user needs to log in or test manually. Rotate if credentials may have leaked:

```bash
opencode-agent rotate-password --reveal <name>
```

## Health Check Interpretation

`opencode-agent status <name>` reports:

- `Local health`: checks the local bind URL, using Basic Auth credentials.
- `Tailnet health`: checks the advertised URL, using Basic Auth credentials when safe.

The remote health check intentionally skips unsafe cases:

```text
skipped credentialed health check for non-loopback HTTP URL
```

This means the advertised URL is remote `http://` and the instance did not opt into `--allow-insecure-remote-http`. Fix by advertising HTTPS through a proxy/tunnel.

Host mismatch:

```text
health URL host "<actual>" does not match expected host "<expected>"
```

This usually means the advertised URL and configured advertise host disagree, or a proxy/tunnel redirects to another host. Check the instance config and proxy settings.

HTTP 401 or 403:

- Credentials may be missing, wrong, stale, or not sent by a manual test.
- Use `status` first; it loads credentials from the keychain.
- Rotate the password if unsure.

Transport errors:

- Local OpenCode may not be running.
- The service may be bound to a different host or port.
- Tailscale Serve/Funnel may be targeting the wrong local port.
- A local firewall or proxy may be blocking access.

## Logs And Service Diagnostics

Agent logs:

```bash
opencode-agent logs --lines 300 <name>
```

macOS LaunchAgent service name:

```text
com.opencode.agent.<name>
```

Linux user service name:

```text
opencode-agent-<name>.service
```

Windows Scheduled Task name:

```text
OpenCodeAgent-<name>
```

If the service starts but OpenCode does not become healthy, the supervisor leaves the process running for diagnostics after the health timeout. Inspect the agent log and OpenCode output for bind, auth, project, or binary errors.

## Safe Remediation Order

Prefer this order when fixing Tailscale exposure:

1. Fix local OpenCode health.
2. Fix Tailscale client state and MagicDNS/DNSName.
3. Reapply exposure with `--dry-run` if uncertain.
4. Reapply Serve/Funnel with the agent command.
5. Restart the instance if bind host changed.
6. Rotate credentials if they were revealed during debugging or may have leaked.

Example reapply:

```bash
opencode-agent expose tailscale <name> --mode serve --dry-run
opencode-agent expose tailscale <name> --mode serve
opencode-agent restart <name>
```
