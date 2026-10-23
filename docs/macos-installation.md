# macOS installation and services

Run the installer normally when the installation prefix is writable by your
account. To install the executable into a protected prefix, use sudo. A sudo
installation without `--service` initializes configuration and synchronizes
subscriptions as the verified invoking user, not as root. If the account cannot
be verified, automatic synchronization is skipped with a warning.

## System service

```sh
sudo sh ./install.sh --service
```

This installs a launchd LaunchDaemon, which starts at boot without an interactive
login. It is a system service running as root, not a per-user LaunchAgent.

| Item | Location |
| --- | --- |
| Service label | `com.runui.oci-mirror-proxy` |
| Definition | `/Library/LaunchDaemons/com.runui.oci-mirror-proxy.plist` |
| Configuration | `/etc/oci-mirror-proxy/config.yaml` |
| Default administration socket | `/var/run/oci-mirror-proxy/admin.sock` |
| Logs | `/var/log/oci-mirror-proxy/stdout.log`, `/var/log/oci-mirror-proxy/stderr.log` |

Existing configuration is preserved, including a custom administration socket.
The daemon recreates its socket directory on startup. Older configurations with
no explicit socket use `/var/run/oci-mirror-proxy/admin.sock` through the service's
runtime environment.

The standard system administration socket is available to all local users without
sudo. Its root-owned directory uses mode `0711` and the socket uses mode `0666`;
these permissions are recreated at each startup. Local users can both inspect
status and change the proxy configuration through the management API. Do not use
this system-service mode on hosts with untrusted local accounts. Private instances
and custom socket paths retain owner-only access.

```sh
sudo launchctl print system/com.runui.oci-mirror-proxy
oci-mirror-proxy status
sudo launchctl kickstart -k system/com.runui.oci-mirror-proxy
```

Use `--update --service` to update and restart an installer-managed service.
Without `--service`, an update does not restart an existing service. Custom or
edited service definitions are not overwritten. If launchd setup fails, the
installer attempts to restore the previous executable and service definition;
configuration is never rolled back. Check any restoration warnings and the logs.

## Docker Desktop

Installing the proxy service does not automatically change Docker Engine settings.
Run Docker Desktop integration as the logged-in user, not through sudo:

```sh
oci-mirror-proxy dockerd plan
oci-mirror-proxy dockerd apply --restart
oci-mirror-proxy doctor
docker pull hello-world:latest
```

`--restart` restarts Docker Desktop and interrupts running containers. Without it,
the settings are saved but activation remains pending. Use `dockerd revert
--restart` to restore settings managed by the tool.

Unlike Linux containerd integration, Docker Desktop configures Docker Hub through
`registry-mirrors`; other registry routes are not automatically configured. The
CLI warns about skipped registries.

Engine settings and a successful pull do not prove that image traffic traversed
the proxy. In particular, verify Docker Desktop/containerd behavior against your
installed version: compare proxy request counters before and after pulling an
uncached image, or capture traffic at the proxy. `doctor` checks configuration
and reachability, not the route taken by an actual image pull. If counters remain
unchanged, do not assume that acceleration is active even when all checks pass.

Non-interactive SSH sessions may omit `/usr/local/bin` from PATH even when an
interactive terminal includes it. Set PATH explicitly before using the CLI or
Docker integration:

```sh
export PATH="/usr/local/bin:$PATH"
```

Client commands discover running services without reading configuration files.
If both a user instance and the system service are running, select the system
service with `--admin-socket /var/run/oci-mirror-proxy/admin.sock`.

## Removing the service

To stop and unregister the service, without removing configuration or binaries:

```sh
sudo launchctl bootout system/com.runui.oci-mirror-proxy
sudo rm /Library/LaunchDaemons/com.runui.oci-mirror-proxy.plist
```

## Permission denied in a user configuration

Older privileged installations or a manual `sudo oci-mirror-proxy serve` may have
left root-owned files in the user's configuration or cache directory. A normal
user cannot open a private root-owned `config.yaml.lock`.

Stop all proxy processes using that configuration before repairing permissions.
Inspect ownership first:

```sh
ls -ld "$HOME/Library/Application Support/oci-mirror-proxy"
ls -la "$HOME/Library/Application Support/oci-mirror-proxy"
ls -ld "$HOME/Library/Caches/oci-mirror-proxy"
```

Only if these are your own application directories, and not shared directories
or symlinks, restore their ownership. Run the cache command only if it exists:

```sh
sudo chown -R "$(id -un):$(id -gn)" "$HOME/Library/Application Support/oci-mirror-proxy"
sudo chown -R "$(id -un):$(id -gn)" "$HOME/Library/Caches/oci-mirror-proxy"
oci-mirror-proxy serve
```

Do not apply these commands to system-service configuration or the entire
Library directory. Do not use `chmod 777` or delete the lock file: configuration
can contain secrets, and a stable lock file coordinates concurrent processes.
The installer does not automatically change ownership of existing user data.
