# Troubleshooting

*[Deutsch](de/troubleshooting.md)*

## Messages in the app and what they mean

Cloud (account, hives, sharing):

| Message | Cause | What to do |
|---|---|---|
| Wrong e-mail or password / Too many attempts | Ten failed sign-ins | Wait 15 minutes or reset the password |
| Confirm your e-mail first | Code not entered yet | Enter the code from the e-mail on the *Confirm e-mail* page. No e-mail, or the code expired: choose *Create account* again with the same e-mail address; an unconfirmed account gets a new code and the old one stops working |
| Unknown or expired code | Hive claim code older than 10 minutes, e-mail confirmation code older than 15 minutes, or mistyped | Run `hive claim` again / for the e-mail, choose *Create account* again with the same address |
| This hive is already claimed | The hive belongs to an account | Remove it there, delete `cloud.json`, claim again |
| Your plan does not include this | A limit (hives, members, shares, sessions) | The box shows the number; upgrade or free a slot |
| The hive did not answer | Hive offline or unreachable | See "Hive offline" below |
| The hive is already removed | Acting on a revoked hive | Claim it again |

Hive (recordings, cameras, live):

| Message | Cause | What to do |
|---|---|---|
| The hive's clock is behind | Computer time behind the cloud by more than a minute | Enable NTP: `sudo timedatectl set-ntp true` (Raspberry Pi has no clock battery) |
| The hive rejected the access token | The hive has not received the cloud's signing keys, or they changed | Restart the hive; check the log for `keys.set` |
| The access token expired | Page open for a long time | Reload |
| The hive has no valid licence yet | Never connected to the cloud since claim | Check it is online on the home page |
| The hive is still starting up | Engine or storage not ready | Wait ten seconds; if it stays, check MediaMTX is installed |
| The hive's video engine reported an error | MediaMTX rejected a camera or path | `engine/mediamtx.log` in the data directory |
| The camera did not answer / refused the login | Network or credentials | `ffprobe` the RTSP URL from another computer |
| Another device answers at that address | Changing a camera's address to one where a different camera or device answers | Check the IP; if you replaced the camera, confirm *It is a new camera* |
| Unexpected error (code) | Something we did not translate | Send the code with your report |

## Hive offline

On the home page the hive shows a grey dot.

1. `hive status` on the hive. If stopped: `sudo hive start`.
2. `journalctl -u camhive-hive -n 50` (Linux) or `logs\hive.log` in the data
   directory (Windows). Look for `connected` or the reason it is not.
3. A hive restarted with a different data directory starts with a **new
   identity** and is not your claimed hive. Make sure the service was installed
   with the same `CAMHIVE_DATA_DIR` you claimed with.
4. Check the clock (`timedatectl`). mTLS fails with a wrong date.
5. Firewall: outbound 8443 and 8444 to `*.camhive.app`.

## Nothing plays, Live works but Archive does not

Live and Archive take different roads (see [Connection](connection.md)). If
Live works and Archive says the hive did not answer:

- Hive → *Settings* → *Connection* → *Details*: is the certificate still *self-signed*? Browsers refuse it
  on the relay road. Wait for the Let's Encrypt one; it is requested on every
  reconnect and every ten minutes.
- Path "Unreachable" with local addresses listed: your router blocks the local
  name. Allow `h.camhive.app` in its DNS rebind settings.

## Cameras not found

- A camera that worked and went offline after a router restart probably got a
  new address. On the hive's own network the hive finds it again within
  minutes (see [When a camera gets a new address](cameras.md#when-a-camera-gets-a-new-address));
  otherwise enter its new address in *Edit* and reserve it in the router.

- Discovery uses multicast on your LAN; cameras on another subnet or on guest
  Wi-Fi are invisible to it. Use the manual address.
- Many cameras need ONVIF switched on in their own settings, sometimes with a
  separate ONVIF user.
- RTSP paths differ per vendor; see the table in [Cameras](cameras.md).

## Port already in use when starting

`listen tcp 127.0.0.1:16016: bind: address already in use` means another hive
process is running, typically a foreground `hive run` next to the service.
Stop one of them.

## Recording stopped

The hive checks once a minute that it records: that the folder it records
into is there, on the same disk as when it started, and takes a small
write; that its database answers without disk errors; and, while a camera
is online, that a new recording started within `recording_stall_minutes`
(default 10) plus one segment's length. A missing, moved or unmounted disk
counts at once; a failed write, database errors and a stall only when the
next check finds them too. When one of these fails, the owner
gets a notification at once ("storage" group, never in a summary; a
further stop within 15 minutes of the last one is told when they are
over, unless the hive records again by then), the app
shows a red banner **Recording stopped at <time>: <reason>** on the hive page
and above a camera's recordings, and `hive status` prints
`NOT RECORDING since …`. The hive tells the cloud directly, without its
disk, and keeps telling it until the cloud confirms; when the app cannot
reach the hive it shows what the hive last told the cloud. When recording
works again, the owner gets "records again" on the same channels.

| Reason | What happened | What to do |
|---|---|---|
| the disk is not mounted | the disk's mount point (from `/etc/fstab`) is an empty folder on the system disk: the disk was absent at boot | connect the disk, `sudo mount -a`, restart the hive |
| the folder is gone | the disk was unplugged, or its mount was lost (a USB disk that dropped out) | connect the disk, mount it (`sudo mount -a` with the fstab line from [An external disk](install-hive.md#an-external-disk-mount-it-from-fstab-by-uuid)), restart the hive |
| on another disk | the disk was mounted again as a new device, or the folder now is on the system disk | restart the hive (`sudo hive restart`) |
| cannot write | the disk is full, read-only after errors, or the folder's permissions changed | see [The disk is failing](#the-disk-is-failing) and `hive status` |
| database errors | `hive.db` answers with I/O or corruption errors | restart the hive; if it stays, the disk is failing |
| no new recording | cameras are online but the video engine writes nothing | `hive diag`, then restart the hive |

The recordings made before the stop stay on the disk; the hive never
forgets them because the disk was away for a while. The other side of
that: if you empty the archive folder by hand, or format the disk and
mount it again without `hive move-data`, the app keeps listing the old
recordings (they no longer play) until a camera writes the first new
one; the hive's log says `archive reconcile … holds no segment` every
minute until then.

## The disk is failing

The hive checks the disk it records to when it starts and every 30 minutes.
It reads SMART through `smartctl` (install `smartmontools`; a USB enclosure is
asked again as SAT), the kernel's I/O errors on that disk
(`journalctl -k`, else `dmesg`), its own and the video engine's failed writes
and a small test write. Pending or uncorrectable sectors, 50 or more
reallocated sectors, a failed SMART check, an NVMe drive past its rated
endurance, kernel I/O errors or failed writes mean **failing**: the hive still
records, the owner gets one notification ("storage" group) and a banner on the
hive page with what it saw. When the test write fails because the disk
cannot be written (an I/O error, or the file system remounted read-only),
the state is **failed**: recording has stopped. A full disk is not a failing
one; that is *storage low*.

What to do: save `hive.key`, `cloud.json`, `hive.yaml`, `archive-key/` and
`hive.db` from the data directory (the banner lists them with their full
paths), then replace the disk with `sudo hive move-data <new-dir>` as in
[Replace the disk](install-hive.md#replace-the-disk). The notification ends
after the disk has looked healthy for a day, or at the first check with a
healthy SMART report and a working test write when only kernel errors
raised it and no new ones came (a disk mounted again after it dropped out).
A failed disk also stops recording: see [Recording stopped](#recording-stopped).

Without `smartctl` and without access to the kernel log the state is
*unknown*, and so it is on file systems whose disk the hive cannot trace
(btrfs subvolumes, ZFS, mergerfs, network shares); on Windows and macOS it
is unknown unless the hive's own writes fail. A disk asleep at a check is
not woken; it is asked again at the next one.

## Logs

| What | Where |
|---|---|
| Hive | `<data>/logs/hive.log` (JSON lines, 20 MB × 5) and the service journal |
| Video engine | `<data>/engine/mediamtx.log` (reset on every engine start) |
| Service journal (Linux) | `journalctl -u camhive-hive -f` |

Passwords in camera URLs are redacted in logs.

## The diagnostic bundle

```bash
sudo hive diag
```

writes `camhive-diag-<timestamp>.zip` into the data directory and lists its
contents: versions, the configuration with secrets masked, cameras without
passwords, disk usage, recent actions and deletions, and the logs. It contains
no video, no keys and no passwords. Read it, then attach it to your report.
Support can also ask the hive for a bundle through the cloud; it lands in the
same place and is noted in *Activity*.
