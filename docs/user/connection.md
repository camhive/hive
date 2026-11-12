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
sharing changes need the cloud. Sign-in needs the cloud.

The hive itself keeps a signed copy of your plan's limits. It stays valid for
seven days and then for another thirty days of grace, refreshed on every
connection, so an internet outage never stops a hive.

## Installing the app

The web app is a Progressive Web App. In Chrome, Edge or Safari choose
"Install Camhive" or "Add to Home Screen". It opens full screen, keeps your
sign-in and updates itself. Video is never cached on the device.

## Mobile networks

Live over a mobile connection usually works peer to peer. Carrier NAT may force
the TURN relay, which costs nothing extra and is not limited. Recordings over
mobile go through the relay and are limited by your home upload speed.
