# Troubleshooting

## Messages in the app and what they mean

Cloud (account, hives, sharing):

| Message | Cause | What to do |
|---|---|---|
| Wrong e-mail or password / account locked | Ten failed sign-ins | Wait 15 minutes or reset the password |
| Please confirm your e-mail | Code not entered yet | Use *Resend code* on the confirm page |
| Unknown or expired code | Claim or confirmation code older than 10 minutes, or mistyped | Run `hive claim` again / request a new code |
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

- Hive → *Settings*: is the certificate still *self-signed*? Browsers refuse it
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
