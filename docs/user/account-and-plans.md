# Account and plans

*[Deutsch](de/account-and-plans.md)*

## Account

*Account* holds everything about you rather than your hives.

- **Password.** At least 10 characters. Changing it signs out every other
  device.
- **Two-factor authentication.** *Enable two-factor* shows a QR code and the
  secret as text; confirm with a code from your authenticator app. You then get
  eight recovery codes, shown once, with a download button. Each recovery code
  signs you in once if you lose the app. Disabling needs your password and a
  current code.
- **Sessions.** Every signed-in device with its browser and last activity;
  revoke any you do not recognise.
- **Forgot password** on the sign-in page mails a link valid for one hour.

Sign-in is throttled per address and locks for 15 minutes after ten failed
attempts.

## Plans

Camhive is one subscription per account; every hive you own is included up to
the plan's count. Cameras are never counted and traffic is never metered.

| | Free | Plus | Pro |
|---|---|---|---|
| Price | 0 | 5 €/month · 50 €/year | 12 €/month · 120 €/year |
| Hives | 1 | 3 | 10 (+2 €/month each beyond) |
| Cameras, local viewing, local export | unlimited | unlimited | unlimited |
| Users of the account (incl. owner) | 3 | 5 | 15 |
| Remote sessions at once | 2 | 4 | 10 |
| Remote quality | sub stream | full | full |
| Sharing recipients | 2 | 5 | 15 |
| Remote export | 5/day, up to 5 min | unlimited, up to 60 min | unlimited, API |
| Notifications | e-mail + push, 1 push device | e-mail + push, 5 push devices | e-mail + push, 20 push devices, + webhooks (planned) |
| Analytics | motion | + object detection (planned) | + faces/plates, search (planned) |
| Early access to updates | | | yes |

*Plan* shows your current plan, add-ons and the comparison table as served by
the cloud, so the numbers there are authoritative if this page lags behind.

**During the beta** paid plans are assigned by us and promo codes work; card
payments arrive later. Enter a code under *Plan* → *Redeem*; each code works
once per account.

**When a plan ends** nothing is deleted. Extra hives go to sleep (they keep
recording and stay reachable on your network), extra members are deactivated,
remote sessions drop to the Free count. Upgrading again restores everything.
You get banners 14, 7 and 1 day before expiry.

## Notifications

Camhive tells the owner of a hive when something needs attention. Open
*Account* → *Notifications* to choose what reaches you and how.

| Event | When it is sent | By default |
|---|---|---|
| A camera is offline | when it has sent no video for 3 minutes; a camera back within that time sends nothing | e-mail and push |
| A hive cannot be reached | when it has been disconnected from Camhive for 5 minutes (power cut, internet down) | e-mail and push |
| Back online | after an offline notification, when the camera or hive is back, on the same channels | as the offline one |
| Storage low | when the archive disk can no longer keep the free space you set; once each time this starts | e-mail and push |
| Motion | when motion starts on a camera that had none for its cooldown (10 minutes unless you change it); after a push the camera stays silent until it has been quiet that long | push (e-mail is off) |

Push needs a device first (below); until then you get e-mail only.

The page shows what the last week brought, for example *This week: 5,600
motion episodes, 312 push notifications.*, so you see the effect of each
setting.

### When notifications come

Motion has a **When**:

- *Each time* (the default): as in the table above.
- *Hourly summary*: one notification at the end of each hour that had
  motion, for example *14 motion episodes on 3 cameras, first 08:12, last
  17:40*. Tapping it opens the hive's recordings of that day.
- *Daily summary*: one notification at midnight for the day.
- *Never*: no motion notifications; the episodes stay on the hive's
  timeline and in the counts.

Summaries are counted per hive and in your time zone. Offline and storage
notifications always come as they happen, because they need you to act.

### Why motion stays calm

A camera that sees motion all day (a street, a tree in the wind) would
otherwise notify you all day. Camhive keeps it down in four ways:

- **Cooldown that waits for calm.** After a push the camera stays silent
  until it has been quiet for its cooldown. A busy street camera pushes
  once when the traffic starts and then goes quiet; a camera that was calm
  for ten minutes notifies again at once. The episodes are all on the
  timeline.
- **At home.** While the owner has the hive open in the Camhive app at home
  (on the hive's network, hives from v0.1.10), the hive marks its motion as
  *quiet*: it is counted and on the timeline, but not pushed. Only the
  owner's app reports, and only while it shows the hive; an app in the
  background or closed stops reporting within a minute, and five minutes
  later motion is pushed again. Members do not count yet.
  The hive never tells the cloud who is at home. It does tell it, with each
  episode, whether that episode is quiet: the cloud's event stream holds
  that for up to 24 hours, and the notifier decides on it within minutes
  and does not keep it (a quiet episode waiting for a summary is only a
  number per hive).
- **Quiet hours** (below) hold motion back until they end.
- **Daily limit.** After 50 motion notifications in a day on your account
  (all hives together), motion comes as one summary an hour per hive until
  midnight. You get one notification saying so.

On the hive itself, each camera has a **motion sensitivity** and calms
itself on a windy day; see [Cameras](cameras.md#motion-sensitivity).

- **Channels.** Every plan includes e-mail and push. Webhooks for Pro are
  planned (#111).
- **Push devices.** Under *Push on your devices*, choose *Get push
  notifications on this device* in each browser or phone that should get
  them, and allow notifications when the browser asks. Free includes one
  device, Plus five, Pro twenty. The list shows your devices; remove
  one you no longer use. After a downgrade, the devices above the new
  limit are kept but get nothing (marked *over your plan's limit*) until
  you upgrade or remove another. *Send a test notification* (once a minute) checks that
  push reaches your devices. Tapping a notification opens the camera (for
  motion, on that day's recordings) or the hive.
- **Signing out** removes this browser from your devices: on a shared
  computer the next person does not get your notifications. Sign in and
  enable push again to get them back.
- **iPhone and iPad** deliver push only to the installed app: in Safari tap
  *Share* → *Add to Home Screen*, open Camhive from the home screen, then
  enable push there (iOS 16.4 or newer).
- **Blocked notifications.** If you once refused, the browser does not ask
  again: allow notifications for the Camhive site in the browser's site
  settings, then enable push again.
- **Quiet hours** hold motion notifications in a daily window, for example
  at night: motion in the window is not pushed, and one summary comes when
  the window ends (*while you slept: 14 motion episodes …*). A summary that
  would fall into the window waits for its end too. Offline and storage
  notifications still come at once, because they need you to act.
- **Per camera.** Open the camera on its hive page and choose
  *Notifications* to switch motion notifications off for that camera or to
  change its cooldown, how long it must be quiet before it may notify you
  again (1 to 60 minutes).
- **Motion that happens while the hive is offline** is not notified later;
  it is on the hive's timeline. A notification about motion arrives within
  seconds or not at all.
- **What the cloud keeps.** For each notification: which hive and camera,
  the camera's name, what kind of event, when, how much space was free
  (storage low only), how motion was seen (camera or picture), and whether
  it was delivered; for each motion episode it heard of: which hive and
  camera, when, and the summary it waits for, so it can count them and keep
  the cooldown; for quiet episodes waiting for a summary only their number
  per hive. Never a picture or video, never who is at home. These records
  are deleted after 30 days.
- **Times** in notifications are in your time zone. The app saves your
  browser's zone each time you change these settings; until you do, German
  accounts get German time and all others UTC.
- Only the owner of a hive gets its notifications; members with shared
  cameras do not yet.
- A hive put to sleep after a plan ended sends no notifications until it is
  active again.

## Activity

*Activity* lists what happened on your account and on each of your hives:
sign-ins, claims, grants, exports, plan changes, with who did it and when.
*Download my data (JSON)* downloads all data we hold about you as JSON.

### A hive's own activity

The hive keeps its own log, on its disk, of what happened to it: cameras
added, changed, moved or removed, exports, deleted recordings, updates
(available, installed, rolled back), archive encryption (switched on,
unlocked, locked, passphrase changed, wrong passphrases), the key file
download, the local emergency sign-in, live views and what the cloud sent
it (licence, certificate, sharing). Open the hive → *Activity* to read it, newest
first, with who did it (you, the Camhive cloud, the updater or the hive
itself; other people's accounts show as their account id), when, and on which camera. *Load more* reaches further back, up to
the last 1000 entries. Only the hive's owner sees this tab; it needs the
hive to be reachable, at home or through the relay.

Above the list, a filter narrows what is shown: the buttons *Cameras*,
*Recordings and exports*, *Updates*, *Sign-ins and access* and *Encryption*
show only those kinds (pick several to combine them, none to see all), and
*Search activity* matches the text of the kind, the camera and who did it.
*Sign-ins and access* also holds the cloud ending a session and diagnostics
sent from the hive. Live views and what the cloud sends the hive (licence,
certificate, relay settings, sharing, remote connections) are written all day
long, so they are hidden until you tick *Show cloud sessions*; the number in
brackets says how many of the loaded entries that also match the search text
are hidden. The filter works on the entries already
loaded: *Load more* keeps it and searches further back. It is part of the
page's address, so a reload or a shared link opens with the same filter.

## Removing a hive

Hive → *Connection* → *Remove this hive* → **Remove hive**. The hive is revoked in the cloud, stops
being reachable remotely and frees its slot in your plan. Recordings stay on
the device. To use the computer again, delete `cloud.json` from its data
directory and run `hive claim`.
