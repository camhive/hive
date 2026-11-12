# Getting started

Camhive keeps your camera footage at home. A small program called a **hive**
runs on a computer in your house (a PC, a NAS, a Raspberry Pi), records your
IP cameras to its own disk and never uploads video anywhere. The Camhive
**cloud** only knows who you are, which hives are yours and who you share
them with. The **web app** at `app.camhive.app` talks to your hive directly
when you are at home and through our relay when you are not.

Three things are always free and never depend on a subscription: recording,
watching on your own network, and exporting clips locally.

## Fifteen minutes to the first picture

1. **Create an account.** Open the app, choose *Sign up*, enter your e-mail and
   a password of at least 10 characters. A 6-digit code arrives by e-mail;
   enter it on the next page, then sign in.
2. **Install a hive** on the computer that will record. Follow
   [Installing a hive](install-hive.md). At the end you run `hive claim`,
   which prints a code like `7K3M-Q2XD`.
3. **Claim it.** In the app choose *Add hive*, enter the code within ten
   minutes and give the hive a name. The page waits until the hive comes
   online, then shows the restart command. Run it.
4. **Add cameras.** Open the hive and choose the *Add camera* tile. Most cameras are
   found automatically over ONVIF; otherwise paste an RTSP address. See
   [Cameras](cameras.md).
5. **Watch.** Open the hive: every camera plays live; click one to open it
   with its recordings on a timeline you can click.

## Where things are

| In the app | What it does |
|---|---|
| Home | Your hives and hives shared with you, with online state |
| Hive → Cameras | Live tiles or list; the opened camera with timeline, playback, export and PTZ; add, edit, switch off, remove cameras (owner only) |
| Hive → Sharing | Who sees which cameras of this hive (owner only) |
| Hive → Connection | How the app reaches the hive; remove the hive |
| Members | People you invited; pending invitations |
| Account | Password, two-factor, signed-in devices |
| Plan | Your plan and limits, promo codes |
| Activity | Everything that happened on your account, downloadable |

The app is available in English and German; switch with the EN/DE selector in
the header. It can be installed on a phone or desktop like a native app, see
[How the app reaches your hive](connection.md).

## What Camhive is not

- It is not cloud video storage. If the recording computer dies, the footage
  on it is gone. Choose a disk you trust and keep the hive running.
- It does not transcode. Whatever your camera sends (H.264 or H.265) is what
  gets recorded and played. Browsers play H.264 everywhere; H.265 playback
  depends on the browser and the device.
- Live video from outside your home goes peer to peer over WebRTC and only
  falls back to our TURN relay when your network blocks direct connections.
