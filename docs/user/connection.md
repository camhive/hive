# How the app reaches your hive

*[Deutsch](de/connection.md)*

The web app has two kinds of traffic to a hive, and they take different roads.

| Traffic | Road |
|---|---|
| Live video | WebRTC from the hive to your browser, peer to peer. At home the app exchanges the connection offer with the hive directly; away from home the cloud passes it along. If the networks block a direct path, the Camhive TURN relay forwards the media. |
| Everything else: recordings, exports, camera settings | HTTPS to the hive's local name when you are on the same network (to its local address when the name cannot be looked up, see [below](#without-internet-opening-the-hive-by-its-address)), otherwise through the Camhive relay |

Hive → *Settings* → *Connection* shows which road is in use ("Direct on your network",
"Direct on your network, by address" or "Through the Camhive relay") and
why, with a *Check again* button; the strip at the top of *Settings* repeats
it as *Path: direct*, *through the relay* or *unreachable*. *Details* lists the
local name, the local addresses, the certificate and the relay.

## The local name

Every hive gets a DNS name such as `hv-8dca14eb9a16560f.h.camhive.app` that
points at its address **inside your home network**. The hive keeps the record
current; the app tries it first with a short timeout and remembers the answer
for ten minutes. On the local road nothing leaves your house, and quality is
never reduced.

Some routers refuse DNS answers that point at private addresses ("DNS
rebinding protection"). The symptom is a *Settings* tab that says the local
name did not answer although local addresses are listed. Allow the domain
`h.camhive.app` in the router (Fritz!Box: Home Network → Network → Network
Settings → DNS Rebind Protection; OpenWrt: `rebind_domain`), or live with the
relay.

Chrome may ask for permission to access devices on your local network the
first time; allow it.

## Certificates

The hive serves TLS with a certificate for its local and relay names. Right
after installation it is **self-signed**, which browsers do not trust, so the
recordings road does not work yet. Within minutes of the first connection the
cloud obtains a **Let's Encrypt** certificate for the hive and hands it over;
the hive's private key never leaves the device. *Settings* → *Connection* →
*Details* shows the current issuer. If it stays self-signed for more than half an hour, see
[Troubleshooting](troubleshooting.md).

## When the cloud is unreachable

Hives work without the cloud. Recording, retention and local viewing continue.
The app keeps a seven-day **offline grant** for every hive you own and the last
list of your hives, so on your own network you can still open live video and recordings
while the cloud is down; a banner says "Local mode". Members' access and
sharing changes need the cloud. Signing in to your Camhive account needs the
cloud.

The hive itself keeps a signed copy of your plan's limits. It stays valid for
seven days and then for another thirty days of grace, refreshed on every
connection, so an internet outage never stops a hive.

## Emergency sign-in on your network

If the cloud stays unreachable for longer than the offline grant lasts (seven
days), or your hive's internet connection is down while you are at home, the
app has no access left from the cloud. For that case you can give the hive a
**local sign-in**: a username and password that only the hive knows. It is
off until you set it up, and it works only on your home network.

Set it up while Camhive is reachable:

1. Open the hive → *Settings* → *Access* → *Local sign-in for emergencies* →
   *Set up local sign-in*.
2. Choose a username and a password of 10 to 256 characters (not the
   username) and press *Save local sign-in*. The hive stores only a hash of
   the password; Camhive never sees it.

Changing the password or pressing *Turn off local sign-in* ends every local
session that is signed in; so does the same change made with `hive
local-login` on the hive's computer. Every change, sign-in and failed attempt
is in the hive's action log (a failed attempt notes whether the username was
right, never what was typed).

A local sign-in reaches cameras and recordings, but not the hive's update
settings: changing them or pressing *Update now* needs a sign-in through
Camhive (updates need the internet anyway). With encrypted recordings (see
[Live and archive](live-and-archive.md#encrypted-recordings)) a local
sign-in can unlock them with the passphrase after a restart, and lock them
again; switching encryption on, changing the passphrase and downloading the
key file need a sign-in through Camhive.

Use it when the cloud cannot reach the hive:

1. Be on the same network as the hive, in a browser where you used the app
   before (it remembers your hives, the hive's local name and its local
   addresses).
2. Open the hive. When Camhive cannot reach it but the hive answers on your
   network, the page shows *Sign in on your network*. If it shows *Open your
   hive by its address* instead, follow the next section first.
3. Enter the local username and password and press *Sign in locally*.

Live video, recordings, exports and camera settings then work as the owner. A
banner says you are signed in locally: sharing, members and your plan need
Camhive and are not available. The local sign-in lasts an hour or until you
close the tab; then sign in again. *Sign out locally* ends it at once.

Limits that protect you:

- The local sign-in is refused through the Camhive relay, from addresses
  outside your local network and from the hive's own computer, so nobody on
  the internet can try it.
- Five wrong passwords in 15 minutes from one device lock that device out for
  15 minutes; fifty from anywhere lock everyone out for 15 minutes. The page
  says how long to wait with a hive newer than 0.1.7; with 0.1.7 or older it
  can only say that the lock lasts up to 15 minutes.
- Anyone on your network who learns the password can watch and change
  everything, as you can. Use a password you use nowhere else.
- Do not make the hive's port 16443 reachable from the internet: no port
  forwarding on the router and no reverse proxy or tunnel in front of it. A
  router or proxy that passes outside traffic on from its own local address
  makes that traffic look as if it came from your network.

**Never set it up and the cloud is gone?** On the hive's own computer, as
administrator, run `sudo hive local-login set --username <name>` (Windows:
`hive.exe local-login set --username <name>` in an administrator prompt) and
type the password twice. `hive local-login status` shows whether it is on,
`hive local-login disable` turns it off.

## Without internet: opening the hive by its address

The hive's local name (for example `hv-8dca14eb9a16560f.h.camhive.app`) is
looked up in the internet's DNS. When your home internet is down, nothing can
look it up. The app also remembers the hive's local addresses (for example
`192.168.1.23`) from the last time Camhive reached the hive, and tries them
when the name does not answer. Browsers trust the hive's certificate for its name only, not
for an address, so the first time you need this (and again after the
certificate renews, about every two months, or the address changes) the hive
page shows *Open your hive by its address*:

1. Open the link (`https://192.168.1.23:16443/`). The browser warns that the
   connection is not private.
2. Check the warning before you go on: it must be exactly the one below for
   your browser, with your hive's local name as the card shows it (here
   `hv-8dca14eb9a16560f.h.camhive.app`).
3. Any other warning — "not trusted", "self-signed", an unknown issuer, or
   any other name — means stop: close that tab. Something other than your
   hive answers at that address, and it would see your local password.
4. The hive's page says your browser can reach it now. Go back to the app: it
   tries again by itself (or press *Try again*) and shows *Sign in on your
   network*. Live video and recordings then work as above.

**Chrome and Edge.** The page shows the code
`NET::ERR_CERT_COMMON_NAME_INVALID`. Under *Advanced* it says: "This server
could not prove that it is 192.168.1.23; its security certificate is from
hv-8dca14eb9a16560f.h.camhive.app." Only then choose "Proceed to
192.168.1.23 (unsafe)". A fake shows `NET::ERR_CERT_AUTHORITY_INVALID` and
"its security certificate is not trusted by your computer's operating
system", or a different name after "is from".

**Firefox.** The page shows the code `SSL_ERROR_BAD_CERT_DOMAIN`. Under
*Advanced* it says the certificate is not valid for 192.168.1.23 and is only
valid for hv-8dca14eb9a16560f.h.camhive.app. Only then choose "Accept the
Risk and Continue". A fake shows `SEC_ERROR_UNKNOWN_ISSUER` or
`MOZILLA_PKIX_ERROR_SELF_SIGNED_CERT`, or names a different site.

**Safari (Mac, iPhone, iPad).** The page says "This Connection Is Not
Private". Choose *Show Details* and view the certificate: it must be issued
to hv-8dca14eb9a16560f.h.camhive.app by Let's Encrypt (ISRG Root X1 or X2)
and must not say that it is not trusted. Only then choose "visit this
website". Safari has not been tested with Camhive yet; if the app still does
not reach the hive afterwards, use Chrome, Edge or Firefox.

Why the check protects you: the hive's certificate is issued publicly for its
name, and nobody else can get one for that name, so a device that pretends to
be your hive cannot produce the same warning.

If the hive got a new address while the internet was down, find it in your
router's device list (Fritz!Box: *Home Network* → *Network*) or run
`hive status` on the hive's computer (the lines `lan: https://…`), type it
into *Hive address* and press *Try again*. Only local IP addresses are
accepted. Giving the hive a fixed address in your router avoids this.

What this does not cover:

- A browser or phone that never opened the app. The app itself comes from
  Camhive; open it once, signed in, on every device you want to use in an
  emergency, and set up the local sign-in while Camhive is reachable.
- A hive whose certificate is not from Let's Encrypt yet (self-signed right
  after installation, see [Certificates](#certificates)): the app offers the
  address only when the certificate is publicly issued, because with any
  other certificate a fake could show the same warning.
- Browsers: tested with Chrome; Edge uses the same engine; Firefox handles
  the warning the same way but was not tested; Safari, iPhone and iPad have
  not been tested yet.

## Installing the app

The web app is a Progressive Web App. It opens in its own window and keeps
your sign-in. Video is never cached on the device.

- **Chrome and Edge** (desktop and Android): when the browser can install
  the app, **Account** shows *Install the app* with an **Install Camhive**
  button; it opens the browser's install dialog. If you decline, the button
  comes back the next time the browser offers it. The browser's own menu
  ("Install Camhive…", "Add to Home screen") works as well.
- **iPhone and iPad**: browsers there have no install event. Account shows
  how: tap *Share* → *Add to Home Screen* (Safari, and since iOS 16.4 also
  Chrome, Edge and Firefox from their share menu), then open Camhive from the
  home screen. Notifications on iOS need this.
- **Safari on a Mac** shows no card; choose *File* → *Add to Dock*.
- Inside the installed app the card is not shown. Firefox on the desktop
  cannot install web apps; it shows nothing.

### New versions of the app

The app looks for a new version when it starts and every hour while it is
open in front (and at once when you bring it back after an hour or more). A new
version downloads in the background and waits: a bar under the header says
*A new version is available* with an **Update** button. The app reloads only
when you tap it, so an update never interrupts a live view or a recording you
are watching. If you leave it, the new version starts the next time you open
the app after closing every Camhive window or tab.

**Icon on the home screen.** When the Camhive logo changes, Android and the
desktop browsers pick up the new icon on their own after the update. An iPhone
or iPad keeps the icon it was added with: remove Camhive from the Home Screen
(touch and hold the icon → *Remove App* → *Remove from Home Screen*) and add it
again with *Share* → *Add to Home Screen*. Your sign-in on that device has to
be entered again afterwards.

## Mobile networks

Live over a mobile connection usually works peer to peer. Carrier NAT may force
the TURN relay, which costs nothing extra and is not limited. Recordings over
mobile go through the relay and are limited by your home upload speed.
