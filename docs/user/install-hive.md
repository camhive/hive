# Installing a hive

A hive is one binary, `hive`, plus the video engine MediaMTX next to it. It
runs as a system service, keeps its data in one directory and talks to the
cloud only after you claim it.

## Requirements

- Linux (x86-64 or ARM64, systemd) or Windows 10/11 (x86-64). Windows on
  ARM is not supported: the video engine has no build for it.
  A Raspberry Pi 4 or 5 with 64-bit Debian works well for a home.
- A disk for recordings. Budget roughly 1 GB per camera per hour at 1080p
  H.264; a 1 TB disk holds about a month of one camera, or a week of four.
- Outbound internet to `gw.camhive.app:8443` and `relay.camhive.app:8444`.
  No port forwarding is needed.
- The computer's clock must be right. Keep NTP on; a clock that is behind
  makes the hive reject access tokens.

## Linux with deb or rpm

```bash
sudo dpkg -i camhive-hive_*_linux_amd64.deb     # or: sudo rpm -i camhive-hive_*_linux_amd64.rpm
```

The package installs the hive and its video engine in `/usr/lib/camhive`
(`hive`, `mediamtx`), links `/usr/bin/hive` there, registers the
`camhive-hive` service and starts it. Data lives in `/var/lib/camhive`, the
configuration in `/etc/camhive/hive.yaml`. Licences are in
`/usr/share/doc/camhive-hive`.

Then claim:

```bash
sudo hive claim --api https://api.camhive.app
```

## Linux from the archive (any distribution, Raspberry Pi)

Download `hive_<version>_linux_<arch>.tar.gz` and unpack it. It contains
`hive` and the video engine `mediamtx`; keep them in the same directory, the
hive looks for the engine next to itself. For example:

```bash
sudo install -m 755 hive mediamtx /usr/local/bin/
```

If recordings should go to another disk (typical on a Raspberry Pi with a USB
drive), set the data directory **when installing**: it is written into the
service definition.

```bash
sudo CAMHIVE_DATA_DIR=/media/usb/camhive hive install
```

```bash
sudo CAMHIVE_DATA_DIR=/media/usb/camhive hive claim --api https://api.camhive.app
```

```bash
sudo hive start
```

Add the disk to `/etc/fstab` so it is mounted before the service starts;
otherwise the hive starts with an empty directory and a new identity.

Everything the hive owns is under the data directory: `hive.yaml`
(configuration), `hive.key` (its identity, never share it), `hive.db`,
`archive/`, `exports/`, `logs/hive.log`, `engine/` (MediaMTX config and log).

## Windows

Unpack `hive_<version>_windows_amd64.zip` into `C:\Program Files\Camhive`
(it contains `hive.exe` and the video engine `mediamtx.exe`; keep them
together), then from an **administrator** PowerShell:

```powershell
cd 'C:\Program Files\Camhive'; .\hive.exe install; .\hive.exe claim --api https://api.camhive.app; .\hive.exe start
```

Data lives in `C:\ProgramData\Camhive`. The service is called `camhive-hive`
and shows as "Camhive Hive" in Services.

## Claiming

`hive claim` prints:

```
Claim code: 7K3M-Q2XD
Enter it in the Camhive app (Add hive) within 10 minutes. Waiting...
```

In the app choose *Add hive*, type the code (case does not matter, the dash is
optional), give the hive a name and confirm. The command then prints
`Claimed. Hive id hv_…` and asks you to restart the service:

```bash
sudo hive restart
```

A hive can be claimed once. To move it to another account, remove it in the
app (Hive → Settings → Remove hive), delete `cloud.json` from the data
directory and claim again. Recordings stay on disk.

## Service commands

| Command | Effect |
|---|---|
| `hive status` | `running`, `stopped` or `unknown` |
| `hive start` / `stop` / `restart` | Control the service |
| `hive install` / `uninstall` | Register or remove the service; footage is kept. The service runs `hive-updater`, which runs the hive (see Upgrading) |
| `hive run` | Run in the foreground (for trying things out) |
| `hive diag` | Write a diagnostic bundle, see [Troubleshooting](troubleshooting.md) |
| `hive version` | Version, commit and build date |
| `hive update` | Ask `hive-updater` to install an available update now (needs root / Administrator) |

With a custom data directory, prefix every command with
`CAMHIVE_DATA_DIR=…`, or the command looks in the default location.

## Ports the hive uses

| Port | Where | Purpose |
|---|---|---|
| 16016/tcp | localhost only | Plain local API |
| 16443/tcp | all interfaces | TLS local API for the web app on your network and for the relay |
| 8890/udp | all interfaces | WebRTC live video |
| 8889, 9996, 9997/tcp | localhost only | MediaMTX internals |
| one free port/tcp, chosen at start | localhost only | Camera streams handed from the hive to MediaMTX |

Open 16443/tcp and 8890/udp on the computer's own firewall if it has one. Nothing
needs to be opened on your router.

## Upgrading

The service runs `hive-updater`, a small process that starts the hive,
restarts it if it stops, and installs signed updates. A new version runs
beside the old one; if it is not healthy within five minutes (the hive
answers and every camera that recorded before records again), the
updater goes back to the old version on its own and never tries that one
again. How it is secured: [Hive updates](../security/updates.md) in the
developer documentation.

Settings in `hive.yaml`:

| Setting | Default | Meaning |
|---|---|---|
| `updates` | `automatic` | `manual`: the hive only tells you an update is available; `hive update` installs it |
| `update_window` | `02:00-05:00` | When automatic updates may install, local time; security fixes do not wait |
| `update_channel` | `stable` | `beta` for earlier releases |
| `update_url` | `https://updates.dev.camhive.app` | Where signed update information comes from; it is checked against keys built into the hive, so the server itself is not trusted |

The updater keeps its versions in `/var/lib/camhive-updater` (Windows:
`C:\Program Files\Camhive\updater`), readable only by root /
Administrators, never in the data directory. Installed updates and
rollbacks appear in the activity log.

**Status:** the updater ships from this version on but installs nothing
yet: the update keys are being set up. Until then, upgrade by hand:

- **deb or rpm:** install the new package. The service, its data directory
  (`CAMHIVE_DATA_DIR`), identity, configuration and recordings are kept;
  a service that ran the hive directly is moved to the updater.
- **Archive:** put `hive`, `hive-updater` and `mediamtx` from the new
  archive in place of the old ones, then `sudo hive install` (moves the
  service to the updater if it was not) and `sudo hive restart`.
