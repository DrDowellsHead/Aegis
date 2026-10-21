# Running Aegis under a service manager (Phase 7.2)

The same executable supports these **two independent** long-running roles:
`aegis start --config FILE --component agent` and `--component receiver`.
This phase does not convert `watch`, `notify`, `history`, or `dashboard`
into managed services; their deployment belongs to phase 7.3.

## Lifecycle and durable delivery

* Linux: systemd sends SIGTERM; macOS: launchd sends SIGTERM.
* Windows: Aegis registers with Service Control Manager (SCM) using
  `golang.org/x/sys/windows/svc`. The `Stop` and `Shutdown` requests
  cancel the same Go `context.Context` used by interactive runs.
* Agent shutdown joins collection and heartbeat workers and closes the
  disk-backed spool lock. **Only a verified durable ACK may remove a
  pending measurement**. An interrupted request may be sent again under
  the same measurement ID after restart. Avoid force-killing the process.
* gRPC receiver cancellation triggers its graceful shutdown. The parent
  service manager waits for Aegis to exit. `Running` indicates that the
  process has started, **not** that PostgreSQL, TLS or remote connectivity
  has passed a health probe. Monitor that separately.
* Exit with an error triggers supervisor restart with delays (on Windows
  through SCM failure actions). A clean operator stop does not restart
  the service. Do not configure unrestricted immediate restart loops.

These templates are **examples**, not installers. They never deploy a
service without the operator's action, and they never delete agent
credentials, spool files or PostgreSQL data.

## Linux: systemd

Provided in `deploy/systemd/aegis-agent.service` and
`aegis-receiver.service`. The units use a dedicated `aegis` service
user, private state under `/var/lib/aegis`, `NoNewPrivileges`,
`ProtectSystem=strict`, and `Restart=on-failure`.

Example (administrator on the target host):

```bash
sudo useradd --system --home-dir /var/lib/aegis --shell /usr/sbin/nologin aegis
sudo install -d -m 0750 -o root -g aegis /etc/aegis
sudo install -d -m 0700 -o aegis -g aegis /etc/aegis/private
sudo install -d -m 0700 -o aegis -g aegis /var/lib/aegis
sudo install -m 0755 aegis /usr/local/bin/aegis
sudo install -m 0644 SERVICE/aegis-agent.service /etc/systemd/system/aegis-agent.service
sudo systemctl daemon-reload
```

Before enabling, create `/etc/aegis/agent.json` (see
`CONFIGURATION.md`); set `queue_dir` explicitly to
`/var/lib/aegis/spool`, ensure `disk` is a readable path, install
the agent's private credentials (0600, owned by `aegis`), and place
the public CA certificate in a readable location. Config JSON can be
root-owned and group-readable, but **never** include credentials in it.

On the receiver host, similarly create `/etc/aegis/receiver.json`,
the private TLS key and authorized agents file. The receiver systemd
unit expects `/etc/aegis/private/database.url`, mode 0600 owned
by `aegis`, and expects PostgreSQL schema migrations already applied.
If PostgreSQL is remote, ignore the local `postgresql.service`
ordering; it is advisory only.

```bash
sudo systemctl enable --now aegis-agent.service
sudo systemctl status aegis-agent.service
sudo journalctl -u aegis-agent.service -f
sudo systemctl stop aegis-agent.service
```

For the receiver, use `aegis-receiver.service` instead. Test
`systemd-analyze verify /etc/systemd/system/aegis-agent.service`
on the deployment host, and ensure the service user can read the
configured disk metric path. Units are not granted privileged host
metrics, extra capabilities, or Docker socket access.

## Windows: SCM

Service names are fixed: **AegisAgent** or **AegisReceiver**.
`aegis.exe` keeps normal console operation when not launched
by SCM; when launched as a registered service, Aegis explicitly
reports `StartPending`, `Running` and `StopPending` and handles
SCM `Stop`/`Shutdown` requests.

1. Extract the Windows archive into e.g. `C:\Program Files\Aegis`
   and protect the executable with normal Program Files ACLs.
2. Provision a **dedicated, unprivileged service account** with
   Windows *Log on as a service* rights. Grant it read access to
   `C:\ProgramData\Aegis\config.json`, the certificate and
   credential files; grant write access only to the agent's spool
   directory and authorized log/state directories. Do not give the
   account administrator rights. Enforce private file ACLs; Unix
   mode bits do not protect Windows files.
3. Open an elevated Windows PowerShell:

```powershell
$account = Get-Credential
.\SERVICE\install-service.ps1 -Component agent `
  -Executable 'C:\Program Files\Aegis\aegis.exe' `
  -Config 'C:\ProgramData\Aegis\config.json' `
  -Credential $account
Start-Service AegisAgent
Get-Service AegisAgent
Stop-Service AegisAgent
```

The script refuses to overwrite an existing service, validates the
config, registers an automatic-start service and sets delayed recovery
actions (5, 15 and 60 seconds). A service-account password is passed
to Windows securely through `PSCredential`, not embedded in
`binPath`. To unregister without deleting data:

```powershell
.\SERVICE\remove-service.ps1 -Component agent
```

For a server receiver, replace `agent` with `receiver` and grant
the account access to the private PostgreSQL URL file specified by
`AEGIS_DATABASE_URL_FILE` in the host environment. The Windows
service template does not set this environment variable itself:
configure it securely for the service account or install a separate
credential-aware launcher in a later deployment phase.

The account must be able to reach the requested Windows volume for
disk usage metrics; mapped interactive-user drive letters may not be
available to services. Verify paths independently.

## macOS: launchd

Two system LaunchDaemon templates are under `deploy/launchd/`:
`com.aegis.agent.plist` and `com.aegis.receiver.plist`. Both request
the unprivileged **aegis** account and keep that identity after
launch. **Provision the account through your organization's approved
macOS administrative process before loading the plist**. No root-run
agent is required. The files reference `/usr/local/bin/aegis`,
`/usr/local/etc/aegis/agent.json` or `receiver.json`,
and `/var/db/aegis` as the working directory. Create the state
directory owned by `aegis`; set the agent's `queue_dir` to a
private subdirectory such as `/var/db/aegis/spool`.

For example:

```bash
sudo install -m 0755 aegis /usr/local/bin/aegis
sudo install -d -m 0700 -o aegis -g aegis /var/db/aegis
sudo install -m 0644 SERVICE/com.aegis.agent.plist /Library/LaunchDaemons/com.aegis.agent.plist
sudo chown root:wheel /Library/LaunchDaemons/com.aegis.agent.plist
plutil -lint /Library/LaunchDaemons/com.aegis.agent.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.aegis.agent.plist
sudo launchctl kickstart -k system/com.aegis.agent
sudo launchctl print system/com.aegis.agent
sudo launchctl bootout system/com.aegis.agent
```

The receiver template references
`/usr/local/etc/aegis/private/database.url` through
`AEGIS_DATABASE_URL_FILE`. Do not put the actual DSN in the plist.
Launchd's `KeepAlive.SuccessfulExit=false` restarts after a failed
exit, not after a clean requested stop; `ThrottleInterval` limits
restart frequency.

## What automated CI proves and what it does not

CI compiles and tests native Go Windows lifecycle handling (including
an in-process simulated SCM stop), validates macOS plist XML,
inspects Linux unit policies, verifies PowerShell template guards and
checks that each platform archive includes matching service templates.
Cross-platform build tests still run with `CGO_ENABLED=0`.

**Not yet tested:** installation into the actual Windows SCM,
Windows ACL and service logon, Linux systemd/SELinux/AppArmor policies,
macOS account provisioning and launchctl bootstrap, sleep/hibernate,
power loss, unattended update, and physical disconnection. Perform
these exercises at phase 7.8 before a stable release.
