# Cameras

Open a hive: its page shows all cameras. Only the owner can add, edit, switch
off or remove cameras; members see the cameras they were granted. *Add camera*
is the last tile (or the last line of the list). Edit, switch off and remove
are on the opened camera and in the list.

## Adding a camera over ONVIF (recommended)

1. *Add camera* → *ONVIF discovery*. The hive scans your network for a few
   seconds and lists the devices that answered.
2. Pick a device, or type its service address by hand
   (`http://192.168.1.50/onvif/device_service` is the usual form). Enter the
   camera's username and password and press *Probe*.
3. The hive reads manufacturer, model and firmware, and lists the camera's
   profiles. The largest profile is preselected as **main**, the smallest as
   **sub**. Give the camera a name and, optionally, a group, and save.

Cameras whose login is wrong answer "The camera refused the login". Cameras
on another VLAN or behind a Wi-Fi isolation are not found by discovery; use the
manual address or the RTSP form.

## Adding a camera by RTSP address

Use this for cameras without ONVIF, or when discovery cannot see them.

| Field | Meaning |
|---|---|
| Name | Required, up to 100 characters |
| Group | Optional label used to sort the list |
| RTSP URL | The **main** stream, `rtsp://host:554/path`. Do not put the login into the URL |
| Sub stream URL | Optional: the camera's second, smaller stream (`rtsp://` or `rtsps://`, no login in the URL); the same login is used |
| Username, Password | The camera's login; the hive stores the password encrypted |

The camera's manual or vendor support page lists the path. Common ones:

| Camera | Main | Sub |
|---|---|---|
| Sonoff CAM-S2 | `rtsp://IP:554/av_stream/ch0` | `rtsp://IP:554/av_stream/ch1` |
| Reolink | `rtsp://IP:554/h264Preview_01_main` | `…_sub` |
| Hikvision / HiLook | `rtsp://IP:554/Streaming/Channels/101` | `…/102` |
| Dahua / Amcrest | `rtsp://IP:554/cam/realmonitor?channel=1&subtype=0` | `subtype=1` |
| TP-Link Tapo | `rtsp://IP:554/stream1` | `stream2` |

A quick check from any computer with ffmpeg installed:

```bash
ffprobe -rtsp_transport tcp "rtsp://user:pass@192.168.1.50:554/av_stream/ch0"
```

Fill in the sub stream when the camera has one (the Sub column of the table
above): grids and remote viewing then use it instead of the full stream. You
can add, change or remove it later in **Edit**; an empty field removes it.
Hives older than v0.1.8 take the sub stream only when the camera is added;
editing it needs v0.1.8.

## Main and sub streams

Every camera has a **main** stream (full quality) and may have a **sub** stream
(reduced). Both are recorded. The app uses sub for medium and small tiles and
for remote viewing on the Free plan; it falls back to the other
stream when a camera has only one.

## Enable, edit, remove

- **Enabled** (*Turn off* / *Turn on* on the opened camera) means the hive
  pulls the camera and records it. Switch it off to stop recording while
  keeping the camera and its history. Switched-off cameras stay visible to the
  owner so they can be switched back on.
- **Edit** changes name, group, login, address and, for RTSP cameras, the sub
  stream URL. Leave the password empty
  to keep the current one. A new address (IP or host name) keeps the camera,
  its recordings and its shares; ports and paths stay. If another device
  answers there, the hive says so and moves the camera only when you confirm
  it is a new camera (then it learns the new device).
- **Remove** deletes the camera from the hive. Its recordings stay on disk
  until retention removes them, by the hive-wide retention: a camera's own
  retention goes with the camera.

## When a camera gets a new address

Home routers hand out addresses that can change, for example after a power
cut. The hive remembers what each camera is: its network card (MAC), and for
ONVIF cameras its device id and serial number. When a camera on the hive's
own network has been offline for two minutes, the hive looks for it: it asks
ONVIF cameras to announce themselves and quietly touches every address of its
network (at most 254) to see which device has which MAC. Then it searches
again after 2, 5 and 15 minutes and hourly while the camera stays missing.

It moves the camera only when the device it found matches everything it
knows and accepts the camera's login. The login is sent only to a device
whose MAC or device id already matches, and only in digest form, never as
plain text. The camera keeps its id, recordings and shares; the timeline
shows a *camera.address_changed* event.

The camera's edit form shows what the hive knows of it and whether it can be
found this way:

- *If the router gives this camera a new address, the hive finds it again* —
  it is on the hive's network.
- *This camera is in another network than the hive* — behind a router or on
  another VLAN. The hive cannot search there; reserve the camera's address
  in your router and change it by hand if it moves.

Reserving camera addresses in the router (DHCP reservation, "always assign
this IP") is the most reliable setup either way. Cameras added before this
version learn their identity on their next connection; until then a move
cannot be healed automatically. Two identical RTSP cameras behind a Wi-Fi
repeater that rewrites MAC addresses cannot be told apart; change their
address by hand.

## Status

Tiles and list show each camera's online state, refreshed every 10 seconds. A
camera is online when its main stream delivers data; two consecutive failed
checks (about 10 seconds) mark it offline. Changes are recorded as events,
visible as marks on the archive timeline and in *Activity*.

## PTZ

Pan-tilt-zoom works for ONVIF cameras that expose a PTZ service. Owners and
operators see a control pad on the opened camera: hold a direction to move, tap a
preset to go there. RTSP-only cameras cannot be moved.

## Recording settings

Recording is configured in `hive.yaml` on the hive, not in the app yet:
segment length (10 minutes), retention in days (30) and the free-space floor
(10 GB) below which the oldest footage is removed first. See the
[configuration reference](../operations/config-reference.md#hive-hiveyaml).

### Keeping one camera's footage longer or shorter

A camera can keep its footage for its own number of days instead of the
hive-wide value: a driveway camera for 90 days, an indoor camera for 3.
The app has no field for it yet; the hive's API takes it
(`PATCH /v0/cameras/{id}` with `retention_days`, owner only):

- a number from 0 to 3650 keeps that camera's footage that many days;
  0 keeps it until the disk needs the space;
- `null` puts the camera back on the hive-wide value.

The change applies at the next retention sweep, within five minutes, without
a restart, and the action log records it. Your plan's longest retention caps
a camera's days as it caps the hive-wide value. The free-space floor still
applies to every camera: when the disk runs low, the oldest footage goes
first, whatever its camera keeps.

A camera's own retention needs a hive newer than 0.1.7. After going back to an older version the hive keeps the setting in
its database but ignores it: every camera follows the hive-wide value, so a
90-day camera under a 30-day hive loses days 30–90 at the next sweep.
The engine ports in `hive.yaml` are ignored the same way.
