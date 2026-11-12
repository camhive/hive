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
| Notifications | basic push, 1 device | rich push + e-mail, 5 devices | + webhooks (planned) |
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

## Activity

*Activity* lists what happened on your account and on each of your hives:
sign-ins, claims, grants, exports, plan changes, with who did it and when.
*Export* downloads all data we hold about you as JSON.

## Removing a hive

Hive → *Settings* → *Remove hive*. The hive is revoked in the cloud, stops
being reachable remotely and frees its slot in your plan. Recordings stay on
the device. To use the computer again, delete `cloud.json` from its data
directory and run `hive claim`.
