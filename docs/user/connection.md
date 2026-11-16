# How the app reaches your hive

The web app has two kinds of traffic to a hive, and they take different roads.

| Traffic | Road |
|---|---|
| Live video | WebRTC from the hive to your browser, peer to peer. At home the app exchanges the connection offer with the hive directly; away from home the cloud passes it along. If the networks block a direct path, the Camhive TURN relay forwards the media. |
| Everything else: recordings, exports, camera settings | HTTPS to the hive's local name when you are on the same network, otherwise through the Camhive relay |

Hive → *Connection* shows which road is in use ("Direct on your network" or
"Through the Camhive relay") and why, with a *Check again* button.

## The local name

Every hive gets a DNS name such as `hv-8dca14eb9a16560f.h.camhive.app` that
points at its address **inside your home network**. The hive keeps the record
current; the app tries it first with a short timeout and remembers the answer
for ten minutes. On the local road nothing leaves your house, and quality is
never reduced.

Some routers refuse DNS answers that point at private addresses ("DNS
rebinding protection"). The symptom is a settings page that says the local
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
the hive's private key never leaves the device. *Connection* shows the current
issuer. If it stays self-signed for more than half an hour, see
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

1. Open the hive → *Settings* → *Local sign-in for emergencies* → *Set up
   local sign-in*.
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
   before (it remembers your hives and the hive's local name).
2. Open the hive. When Camhive cannot reach it but the hive answers on its
   local name, the page shows *Sign in on your network*.
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
  says how long to wait.
- Anyone on your network who learns the password can watch and change
  everything, as you can. Use a password you use nowhere else.
- Do not make the hive's port 16443 reachable from the internet: no port
  forwarding on the router and no reverse proxy or tunnel in front of it. A
  router or proxy that passes outside traffic on from its own local address
  makes that traffic look as if it came from your network.

What it needs from the internet: the hive's local name (for example
`hv-8dca14eb9a16560f.h.camhive.app`) is answered by Camhive's DNS. If your
home internet is down, or the DNS cannot be reached, your browser cannot look
the name up and the emergency sign-in does not work, even though the hive
records and is on your network. Recording is not affected. A way into the
hive that does not depend on DNS is planned (#101).

**Never set it up and the cloud is gone?** On the hive's own computer, as
administrator, run `sudo hive local-login set --username <name>` (Windows:
`hive.exe local-login set --username <name>` in an administrator prompt) and
type the password twice. `hive local-login status` shows whether it is on,
`hive local-login disable` turns it off.

## Installing the app

The web app is a Progressive Web App. In Chrome, Edge or Safari choose
"Install Camhive" or "Add to Home Screen". It opens full screen, keeps your
sign-in and updates itself. Video is never cached on the device.

## Mobile networks

Live over a mobile connection usually works peer to peer. Carrier NAT may force
the TURN relay, which costs nothing extra and is not limited. Recordings over
mobile go through the relay and are limited by your home upload speed.
