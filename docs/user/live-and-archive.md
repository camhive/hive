# Live and archive

Live video, recordings and camera management share one page: open a hive from
*Hives* and you see all its cameras.

## Live

Every camera is a live tile. The switch next to the hive name picks the tile
size, drawn as little grids: **large**, **medium**, **small**, or a **list**
without video, which costs almost no traffic on mobile data. The choice is
remembered on this device. There are no pages: scroll to see more cameras.
Only tiles on screen stream; a tile scrolled away stops until it comes back.
Each tile reports its state: Connecting, Live, Reconnecting, Camera offline or
Disabled, and has a fullscreen button.

Click a camera to **open** it. It moves to the top in a large player with the
day's recordings on a timeline underneath (see [Archive](#archive)); the other
cameras stay below in the size you chose. Close it with ×.

Every tile and every line of the list has a **Recordings** button (clock
icon). It opens the camera at its timeline instead of live: pick a moment on
the timeline to play it, or *Back to live*.

- **Quality.** The label *Full* or *Reduced (plan)* tells you which stream you
  get. Large tiles and the opened camera use the main stream; medium and small
  tiles use the sub stream for everyone; remote viewing on the Free plan uses
  the sub stream too. At home on your own network you always get full quality.
- **Path.** Live video is WebRTC straight from the hive to your browser. At
  home the app sets the connection up with the hive directly; away from home
  the Camhive cloud passes the connection offer along. When both sides sit
  behind strict NAT the stream goes through the Camhive TURN relay; nothing
  is stored on the way.
- **Reconnects** back off from 1 to 30 seconds. A camera that is offline on
  the hive shows as such instead of spinning.
- **Session evicted.** Your plan allows a number of simultaneous remote
  sessions. When a newer sign-in pushes you out, the page says so; close the
  other device or upgrade.
- **Sound.** A camera that records sound (*Record sound* in the camera's
  *Edit* form, owners only) and has a microphone gets a speaker button on
  its tile, next to fullscreen, so it stays there in fullscreen too. Live
  starts muted, because browsers do not play sound on their own; tap the
  button to hear it. The choice is remembered per camera on this device. If
  the browser still refuses sound when the page opens, the picture keeps
  playing, the button shows the sound off again and one tap turns it on.
  Cameras without sound show no button. Each tile has its own button, so
  several cameras can play sound at the same time.
- **PTZ.** Owners and operators get a pad on the opened camera when it
  supports PTZ, see [Cameras](cameras.md#ptz).

## Archive

The opened camera plays recordings from the hive's disk; there is no separate
archive page. It starts live (or at the timeline, when opened with
**Recordings**), and the timeline below shows today.

1. Click the **timeline** to play the recording from that moment. *Back to
   live* returns to live video.
2. The **days** below the timeline run from the oldest footage to today, in
   your browser's time zone; days with footage are highlighted, the others
   cannot be picked. Picking an earlier day opens its recordings. *Stream*
   switches between main and sub. Away from home on a plan with reduced
   remote quality the timeline and playback always use the sub stream's
   recordings.
   Playback starts silent. *Sound off/on* plays the recorded sound next to
   the picture for cameras that record it; it is fetched only while sound is
   on (about 1 MB a minute). Exports always keep the sound. *Speed* plays at
   1×, 2×, 4× or 8×; *Snapshot* saves the frame on screen as a PNG file on
   your device (it is not sent anywhere). Playback continues into the next
   recording of the day on its own.
3. The **timeline** shows the day: recorded spans as blocks, **motion** as
   red bands as long as it lasted, **storage low** as a red line across the
   bar where it began (it concerns the whole hive, so every camera shows
   it), camera status changes (online/offline) as small grey marks, an
   *activity* wave (how much the picture changed; a hint, not motion
   detection) and, for cameras that record sound,
   a *sound level* line, and the playhead; the time it shows, with seconds,
   stands above the bar and follows playback. Hover to read the time under
   the cursor; click to seek. Zoom with the mouse wheel, two fingers or the
   buttons (whole day, 6 hours, 1 hour around the playhead); + and − work
   too. Zoomed in, the bar follows the playhead and dragging it (or swiping
   on a phone) pulls the footage under the playhead; over the whole day,
   dragging moves the playhead. Letting go plays from there. Arrow keys step
   a minute, with Shift ten minutes.
4. Clicking a gap jumps to the next recorded moment; if nothing follows, the
   page says so.
5. **Events (n)** under the timeline lists the day's events, newest first:
   motion with how long it lasted, storage low, and camera status changes.
   Click one to play the recording from that moment. Motion that is still
   going on says *ongoing*.

### Motion

The hive marks motion for every camera; nothing is sent to the cloud for it
and no plan is needed. It takes motion from one of two places, chosen per
camera:

- **Seen by the camera.** A camera added over ONVIF that offers events
  reports motion itself; once it does, the hive uses that. Switch motion
  detection on in the camera's own settings and set its zones and
  sensitivity there. With detection off the camera says nothing and the
  hive keeps watching the picture instead. A recorder (NVR) added as one
  camera reports the motion of all its channels together.
- **Seen in the picture.** For every other camera (added by RTSP address, or
  without ONVIF events) the hive watches how large the compressed frames of
  the sub stream are: a still scene is small, movement makes the frames
  several times larger. Nothing is decoded, so it costs almost no CPU, even
  on a Raspberry Pi. It is a good hint, not an alarm system: a camera set to
  *constant bitrate* (CBR) shows little, heavy noise at night hides small
  movement, a light switched on can look like motion for a second, and
  movement that goes on for more than about a minute and a half fades into
  the background. Set the camera's sub stream to *variable bitrate* (VBR)
  for the best results.

Short pauses (under 10 seconds) count as one motion, and one motion lasts
at most 30 minutes; the event list shows which way each was seen.

Playback streams fragmented MP4 directly from the hive and starts within a
second or two on the local network. Over the relay it depends on your upstream
bandwidth at home.

## Export

Owners and operators can cut a clip from the opened camera. Seek to the
start, choose *Export from here*, enter the length in minutes and start. The
hive writes an MP4 of the chosen stream without re-encoding and lists it under
the player; download it from there. The maximum length comes from the owner's plan: 5 minutes on
Free, 60 on Plus, unlimited on Pro. Local export on your own network is never
limited by plan.

The list of exports is per user and survives a restart of the hive. An export
that was still running when the hive stopped shows as failed; start it again.
Finished exports are kept for 7 days and up to 10 GB in total by default;
after that the oldest go first. The owner can change both limits in
`hive.yaml` (`export_days`, `export_max_gb`). Download what you want to keep.

## Retention

The hive deletes footage older than the configured number of days (30 by
default) and, independently, keeps at least the configured free space on the
recording disk: old exports go first, then the oldest complete segments. Recording never
stops because of retention; if the disk cannot be freed, the hive reports
"storage low" and keeps going; the timeline of every camera shows when it
began and, once space is back, when it ended.
Downgrading a plan never deletes anything.

## Deleting recordings

The owner can remove footage from the hive's disk at once, for example when
someone who was filmed asks for it to be erased (GDPR Art. 17). Members,
operators included, cannot. It works on the home network without the cloud.

- **Hive → Connection → Delete recordings** picks the cameras (or *all
  cameras*, which also covers footage of cameras removed since) and what to
  delete: *everything*, *a time range*, or footage *older than* a number of
  days. *Also delete exports of this footage* removes exports that overlap
  it too; otherwise those copies stay.
- In an opened camera, **Delete this day…** starts the same form with that
  camera and day filled in.
- **Preview** shows what the hive will really delete, per camera: the span,
  the number of files and their size. Recordings are files of a few minutes
  (`segment_minutes`); a file that touches the chosen range is deleted
  whole, so the span can be a little wider than chosen, never narrower.
  Both streams (main and sub) always go together.
- **Delete permanently** deletes right away and frees the space. There is
  no trash and no undo. Deleting *everything* asks you to type the camera's
  name (one camera) or the hive's name first.
- The file being recorded right now is deleted within minutes, as soon as
  the hive closes it; the preview and the result say how many there are.
  If an export of the footage is still being made, the hive refuses until
  it has finished.
- Every deletion is recorded in the hive's own action log (who, which
  cameras, which span, how much), which cannot be deleted. The app does not
  show that log yet; the owner can read it from the hive's API
  (`GET /v0/actions`).

## Encrypted recordings

The owner can have the hive encrypt its recordings, so that whoever takes
the box or its disk cannot play them. It is off until the owner switches it
on, and it cannot be switched off again. The owner finds it on the hive's
**Connection** tab under **Encrypted recordings** → **Manage
encryption…** (it needs hive version 0.1.7 or later).

- **Switching it on.** The app first says what it means: without the
  passphrase *and* the key file nobody can play the recordings again,
  Camhive included, and encryption cannot be switched off. After **I
  understand, choose a passphrase**, type a passphrase of 10 to 256
  characters twice and choose **Switch encryption on**. The app then asks
  you to **Download key file** right away and finishes only after you did;
  keep the file somewhere other than the hive (a USB stick, your password
  manager). The key file alone is useless to a thief. From then on every
  recording is encrypted a few seconds after its file closes (files are 2
  minutes long while encryption is on), and so are the recordings and
  exports already on the disk, in the background.
- **After every restart** of the hive (an update, a power cut) the archive
  is **locked**. The hive records as always and live view works, and the
  timeline still shows when there are recordings, but playback, sound and
  exports wait until the owner unlocks the archive with the passphrase.
  The hive page says so: the owner taps **Unlock**, types the passphrase and
  the recordings play again; members read that the archive is locked by its
  owner. The same notice sits under an opened camera, where the sound and
  export buttons are greyed out while it is locked. On the Connection tab
  the owner can also **Lock recordings** again at any time. Five wrong
  passphrases in 15 minutes block unlocking for 15 minutes; with a hive
  newer than 0.1.7 the app says how long is left, with 0.1.7 itself only
  that it is up to 15 minutes. Unlocking and locking also work with the emergency sign-in on
  your network ([Connection](connection.md)); switching encryption on,
  changing the passphrase and downloading the key file need a sign-in
  through Camhive, and the app says so instead of offering them.
- **Changing the passphrase** (**Change passphrase…**: the current one, then
  the new one twice) keeps the recordings as they are. A key file downloaded
  earlier still opens with the old passphrase, so the app offers **Download
  the new key file** right after the change.
- **If the app warns that new recordings are stored unencrypted,** the
  hive could not read the archive key from its disk. Unlocking the
  recordings writes it again and encryption resumes.
- **Exports** are stored encrypted on the hive and download as normal MP4
  files while the archive is unlocked.
- **What a stolen disk still shows:** the last minutes of each camera that
  were not yet encrypted, when and how much each camera recorded (not what),
  and the events. While the archive is unlocked the key is in the hive's
  memory: a computer that hibernates, or swaps memory to disk, can write it
  there. On Windows, crash dumps an administrator switched on can hold it too.
