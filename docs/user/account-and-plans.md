# Account and plans

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
| Members besides the owner | 2 | 4 | 14 |
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
| Motion | when motion starts on a camera; then not again for that camera until its cooldown (10 minutes unless you change it) has passed | push (e-mail is off) |

Push needs a device first (below); until then you get e-mail only.

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
- **Quiet hours** stop motion notifications in a daily window, for example
  while you are at home in the evening. Offline and storage notifications
  still come, because they need you to act.
- **Per camera.** Open the camera on its hive page and choose
  *Notifications* to switch motion notifications off for that camera or to
  change how often it may notify you (every 1 to 60 minutes).
- **Motion that happens while the hive is offline** is not notified later;
  it is on the hive's timeline. A notification about motion arrives within
  seconds or not at all.
- **What the cloud keeps.** For each notification: which hive and camera,
  the camera's name, what kind of event, when, how much space was free
  (storage low only), how motion was seen (camera or picture), and whether
  it was delivered. Never a picture or video. These records are deleted
  after 30 days.
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
*Export* downloads all data we hold about you as JSON.

## Removing a hive

Hive → *Settings* → *Remove hive*. The hive is revoked in the cloud, stops
being reachable remotely and frees its slot in your plan. Recordings stay on
the device. To use the computer again, delete `cloud.json` from its data
directory and run `hive claim`.
