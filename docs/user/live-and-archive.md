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
- **PTZ.** Owners and operators get a pad on the opened camera when it
  supports PTZ, see [Cameras](cameras.md#ptz).

## Archive

The opened camera plays recordings from the hive's disk; there is no separate
archive page. It starts live, and the timeline below shows today.

1. Click the **timeline** to play the recording from that moment. *Back to
   live* returns to live video.
2. The **days** above the timeline list days with footage, in your browser's
   time zone; picking an earlier day opens its recordings. *Stream* switches
   between main and sub. Away from home on a plan with reduced remote quality
   the timeline and playback always use the sub stream's recordings.
   Playback in the browser is silent: cameras send audio in formats browsers
   cannot play, so the hive sends the video only. Exports keep the sound.
3. The **timeline** shows the day: recorded spans as blocks, camera events
   (online/offline) as marks, and the playhead with its time. Hover to read the
   time under the cursor; click to seek. Zoom buttons switch between the whole
   day, 6 hours and 1 hour around the playhead; the ruler labels adapt. Arrow
   keys step a minute, with Shift ten minutes.
4. Clicking a gap jumps to the next recorded moment; if nothing follows, the
   page says so.

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
"storage low" and keeps going. Downgrading a plan never deletes anything.
