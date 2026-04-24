# opencode-serve

`opencode-serve` is a single-file Python wrapper for running OpenCode on
loopback and exposing it to a tailnet through Tailscale Serve.

The whole project is the executable script plus this README.

## What it does

- Finds the local `opencode` binary.
- Starts `opencode serve` on `127.0.0.1` with generated basic-auth credentials.
- Stores the generated password in the OS keychain.
- Creates a Tailscale HTTPS Serve entry that forwards to the loopback server.
- Installs a user service so the instance starts on login.
- Supports multiple named instances.

On macOS it uses `launchd` and Keychain. On Linux it uses user `systemd` and
`secret-tool` when available, with a mode-600 password file fallback.

## Requirements

- Python 3.
- OpenCode installed and available as `opencode`, or at
  `~/.opencode/bin/opencode`.
- Tailscale installed, logged in, and running.
- Tailscale MagicDNS and HTTPS certificates enabled for the tailnet.

## Install an instance

```sh
./opencode-serve install --reveal
```

This creates the default instance, starts it, and prints the tailnet URL,
username, and generated password. Omit `--reveal` if you do not want the
password printed during installation.

To create a named instance:

```sh
./opencode-serve install --name work --reveal
```

## Common commands

```sh
./opencode-serve status
./opencode-serve logs
./opencode-serve show-password
./opencode-serve restart
./opencode-serve stop
./opencode-serve start
```

Every command accepts an optional instance name:

```sh
./opencode-serve status work
./opencode-serve logs work --lines 200
```

## Uninstall

Stop the service and remove the Tailscale Serve entry:

```sh
./opencode-serve uninstall
```

Remove service, Tailscale Serve entry, config, state, and stored password:

```sh
./opencode-serve uninstall --purge
```

For a named instance:

```sh
./opencode-serve uninstall work --purge
```

## Files

Per-instance config:

```text
~/.config/opencode-serve/<name>.json
```

Per-instance logs and local state:

```text
~/.local/state/opencode-serve/<name>/
```

macOS service file:

```text
~/Library/LaunchAgents/com.opencode.serve.<name>.plist
```

Linux service file:

```text
~/.config/systemd/user/opencode-serve-<name>.service
```

## Security model

OpenCode only listens on `127.0.0.1`. Remote access is provided by Tailscale
Serve over HTTPS inside the tailnet. The OpenCode server is protected with a
generated username/password pair, and the password is stored outside the repo in
the OS credential store when possible.

If `~/.opencode/opencode.json` does not exist, the installer creates a
conservative default config that asks before tool use except for basic read and
search operations.
