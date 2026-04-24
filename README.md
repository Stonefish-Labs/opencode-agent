# opencode-serve

`opencode-serve` is a single-file Python wrapper for running OpenCode on
loopback and exposing it to a tailnet through Tailscale Serve.

The whole project is the executable script plus this README.

## What it does

- Finds the local `opencode` binary.
- Starts `opencode serve` on `127.0.0.1` with generated basic-auth credentials.
- Stores the generated password outside the repo.
- Creates a Tailscale HTTPS Serve entry that forwards to the loopback server.
- Installs a user service so the instance starts on login.
- Supports multiple named instances.

On macOS it uses `launchd` and Keychain. On Linux it uses user `systemd` and
`secret-tool` when available, with a mode-600 password file fallback. On Windows
it uses Task Scheduler and the same mode-600 password file fallback.

## Requirements

- Python 3.
- OpenCode installed and available as `opencode`, or at
  `~/.opencode/bin/opencode`.
- Tailscale installed, logged in, and running.
- Tailscale Serve available for the tailnet.
- Tailscale MagicDNS and HTTPS certificates enabled for the tailnet.

## Tailscale setup

Before installing an instance, make sure the tailnet can issue HTTPS
certificates for `*.ts.net` machine names:

1. Open the Tailscale admin console.
2. Go to DNS.
3. Enable MagicDNS.
4. Under HTTPS Certificates, enable HTTPS.
5. Accept the notice that machine names and the tailnet DNS name are published
   in Certificate Transparency logs.

`opencode-serve` configures Tailscale Serve with an HTTPS listener. If Serve has
not been enabled or consented for the tailnet yet, the `tailscale serve` command
may print a Tailscale login or consent URL. Open that URL, approve the change,
then rerun:

```sh
./opencode-serve install --reveal
```

The browser certificate for the printed URL should be issued by Let's Encrypt
and match the machine DNS name, for example:

```text
desktopml.tail9749c7.ts.net
```

If the certificate is for `localhost`, a router, a self-signed issuer, or any
other hostname, the browser is not seeing the Tailscale Serve HTTPS certificate.

## Install an instance

```sh
./opencode-serve install --reveal
```

On Windows, run the same command through Python:

```powershell
python .\opencode-serve install --reveal
```

This creates the default instance, starts it, and prints the tailnet URL,
username, and generated password. Omit `--reveal` if you do not want the
password printed during installation.

To create a named instance:

```sh
./opencode-serve install --name work --reveal
```

## Ubuntu home server setup

Ubuntu is supported through per-user `systemd` services. Install the basic
runtime pieces first:

```sh
sudo apt install python3 dbus-user-session
```

For a home server that should start `opencode-serve` after reboot before you
SSH in, enable lingering for the account that will run OpenCode:

```sh
sudo loginctl enable-linger "$USER"
```

Then install normally:

```sh
./opencode-serve install --reveal
```

On a headless server, `secret-tool` may not have a desktop keyring available.
In that case, `opencode-serve` falls back to a mode-600 password file under the
instance state directory:

```text
~/.local/state/opencode-serve/<name>/.pw
```

That fallback is expected for a single-user server account. OpenCode still binds
only to `127.0.0.1`; Tailscale Serve is the remote HTTPS entry point.

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

Windows scheduled task:

```text
opencode-serve-<name>
```

## Security model

OpenCode only listens on `127.0.0.1`. Remote access is provided by Tailscale
Serve over HTTPS inside the tailnet. The OpenCode server is protected with a
generated username/password pair, and the password is stored outside the repo in
the OS credential store when possible.

If `~/.opencode/opencode.json` does not exist, the installer creates a
conservative default config that asks before tool use except for basic read and
search operations.
