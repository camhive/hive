# Sharing

*[Deutsch](de/sharing.md)*

Sharing has two steps: a person becomes a **member** of your account, then you
**grant** them cameras on a hive. Members never pay; the owner's plan covers
everyone they share with.

## 1. Invite a member

*Members* → *Invite someone* → e-mail → *Send invitation*. The person gets an
e-mail with a link valid for seven days. The link is also shown on the page,
so you can pass it on yourself if the mail is slow.

The invitee opens the link, signs in or creates an account **with the invited
e-mail address**, and accepts. They now appear under *Members*. Pending
invitations can be cancelled; members can be removed, which ends all their
access at once and notifies them.

## 2. Grant cameras on a hive

Open the hive → *Sharing* → *Share cameras*:

| Field | Meaning |
|---|---|
| Member | One of your accepted members |
| Camera | A single camera or *all cameras* of this hive |
| Role | **viewer** watches live and recordings; **operator** can also move PTZ cameras and export clips |
| Expires | Optional end date, for a house-sitter or a holiday |

The grant appears in the list with a *Revoke* button. The hive learns about
grants from the cloud immediately and keeps a copy, so sharing keeps working
on your network even when the cloud is unreachable.

## Expiring grants: extend or make permanent

A grant with an end date counts as **expiring** during its last three days.
From then on:

- you and the member each see one notice for it on the home page (it ends on
  …), and the grant is marked *expiring* in the hive's *Sharing* list;
- you and the member each get **one e-mail** about it, when the account can
  receive e-mail notifications. It is not repeated, also not after a restart
  of the cloud, and nothing is sent through the invitation link. A grant whose
  end you set less than three days ahead (a weekend share, or an extension by
  a day or two) gets no such e-mail: you just chose the date. It still shows
  the notice in the app.

In *Sharing* the grant has two actions: *Extend* (pick a new end date) and
*Make permanent* (remove the end date). A new end must lie in the future and
at most ten years ahead; for longer, make the grant permanent. Either one keeps the member watching
without interruption: their session stays, and the hive gets the new grant at
once. Changing anything else (role, an earlier end) interrupts the member's
session on that hive once; the app then connects again with the new access.

When the end date passes, the grant is **expired**: the hive stops honouring
it at that moment, and the member loses access to those cameras. The grant is
not deleted. It stays in your *Sharing* list as *ended* with its date, so
*Renew* (a new end date) or *Make permanent* brings it back in one step;
*Revoke* removes it. The notice on
the home page says the grant has ended for three more days, then disappears
(the grant itself stays listed for you). A new end date is a new expiry: you
get one notice and one e-mail for it again when its last three days begin.

## What members see

On their home page your hive appears under *Shared with me*. They get live video
and recordings for the granted cameras, and PTZ and Export if they are operators.
They cannot add or change cameras or see your sharing, and the *Settings*
tab shows them only how the app reaches the hive, the hive's version and
the storage of their cameras, without your rows (disk health, encryption,
deleting recordings, moving to a new disk, local sign-in, updates, removing
the hive).
A grant of theirs that ends within three days, or ended less than three days
ago, shows as a notice on their home page; to keep access they ask you to
extend it.

## Limits

| | Free | Plus | Pro |
|---|---|---|---|
| Members (besides you) | 2 | 4 | 14 |
| Sharing recipients | 2 | 5 | 15 |
| Roles | viewer, operator | viewer, operator | viewer, operator |

Reaching a limit shows a box with the number and a link to *Plan*. Downgrading
deactivates extra members instead of deleting anything; upgrading restores
them without new invitations.
