# Camhive hive

The hive is the Camhive agent that runs on your own computer, next to your
IP cameras. It records them to your disk through the MediaMTX video engine,
serves live and recorded video to the Camhive web app and never sends video
to the cloud. This repository holds everything that runs on your side: the
hive, the camera simulator used in tests, the hive's local API, the web app
and the user documentation.

The Camhive cloud (accounts, sharing, plans, relay) is not part of this
repository. The hive talks to it over mutually authenticated TLS with
per-device certificates and signed access tokens; see `internal/protocol`,
`internal/tunnel` and `internal/auth`.

This repository is exported automatically from the Camhive development
repository; every commit here corresponds to one commit there. Please open
issues and security reports here; they reach the same team.

## Build and test

```bash
go build -o bin/hive ./cmd/hive
```

```bash
tools/fetch-mediamtx.sh bin   # the pinned video engine, checksum verified
```

```bash
go test ./...
```

Release archives and packages are built with GoReleaser
(`goreleaser release --snapshot --clean`). Builds are reproducible: the same
commit and toolchain give the same binary, so a release can be checked
against this source.

The web app lives in `web/` (`pnpm install && pnpm -r build`). Its client for
the cloud API is generated from a specification that is not published here;
the generated types are included.

## Documentation

How to install and use the hive: [docs/user](docs/user/README.md).

## Licence

The hive, camsim and the web app are licensed under the GNU Affero General
Public License, version 3 (AGPL-3.0); see [LICENSE](LICENSE). Third-party components are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
