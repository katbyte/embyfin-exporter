# embyfin-metrics - Emby and Jellyfin state as a Prometheus metrics page

[![GitHub release](https://img.shields.io/github/v/release/katbyte/embyfin-metrics?color=blueviolet)](https://github.com/katbyte/embyfin-metrics/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/embyfin-metrics?color=00ADD8)](https://github.com/katbyte/embyfin-metrics/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/embyfin-metrics?color=blue)](https://github.com/katbyte/embyfin-metrics/blob/main/LICENSE)
![build](https://github.com/katbyte/embyfin-metrics/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/embyfin-metrics/actions/workflows/pr-tests.yaml/badge.svg)
![acceptance](https://github.com/katbyte/embyfin-metrics/actions/workflows/pr-acceptance.yaml/badge.svg)
![lint](https://github.com/katbyte/embyfin-metrics/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/embyfin-metrics/badges/coverage.json)](https://github.com/katbyte/embyfin-metrics/actions/workflows/coverage.yaml)

A small program that reports what an [Emby](https://emby.media) and a [Jellyfin](https://jellyfin.org) server are doing, as a page [Prometheus](https://prometheus.io) can read.

**It only reads, and it names nobody unless you ask.** It never changes anything on a server. Names of people, titles and devices are on the page only when a setting asks for them.

One container covers both servers, or either alone.

## Install

```bash
docker pull ghcr.io/katbyte/embyfin-metrics:latest     # or
go install github.com/katbyte/embyfin-metrics@latest   # or
brew install katbyte/tap/embyfin-metrics
```

## Quick start

Give it a server's address and an API key from that server's dashboard:

```bash
export EMBYFIN_METRICS_EMBY_SERVER=http://nas:8096
export EMBYFIN_METRICS_EMBY_TOKEN=...

embyfin-metrics print   # ask each server once and print the page: a check that the address and key work
embyfin-metrics serve   # serve the page at :9096/metrics, with a health probe at /healthz
```

With Docker, [`docker-compose.yml`](docker-compose.yml) runs the released image:

```bash
cp .env.example .env   # then put the API keys in it
docker compose up -d
curl -s http://127.0.0.1:9096/metrics
```

Then point Prometheus or Grafana Alloy at port 9096. [docs/scraping.md](docs/scraping.md) has a config for each and queries to start from.

## What is on the page

Every reading starts `embyfin_` and has a `server` label, `emby` or `jellyfin`. [docs/readings.md](docs/readings.md) lists all 58 with their labels.

| Group | What it says |
|---|---|
| [The server](docs/readings.md#the-server) | Whether it answers, its version, and whether a restart or an update is waiting. |
| [Sessions and streams](docs/readings.md#sessions-and-streams) | Devices playing, paused or idle, what kind of thing is playing, and how it reaches the device. |
| [Why a stream is made](docs/readings.md#why-a-stream-is-made) | The server's reasons for converting a file, and what the streams it makes come to. |
| [Who is streaming now](docs/readings.md#who-is-streaming-now) | A line for each stream: how fast, how far through, and from home or outside. |
| [Scheduled tasks and scans](docs/readings.md#scheduled-tasks-and-scans) | Tasks running or failed, and how the library scan is getting on. |
| [Plugins](docs/readings.md#plugins) | How many each server has, their versions, and which have a newer version on offer. |
| [Libraries](docs/readings.md#libraries) | Items in each library by kind, and on Emby how many are new. |
| [Accounts and devices](docs/readings.md#accounts-and-devices) | How many there are, and how many were seen in the last day, week and month. |
| [Sign-ins](docs/readings.md#sign-ins) | Sign-ins and refused tries, from home or from outside. |
| [What was played](docs/readings.md#what-was-played) | Time and plays each week, plays that ended within a minute, and on Emby why streams were made. |

What was played comes from each server's [Playback Reporting](https://github.com/faush01/playback_reporting) plugin. A server without it says so on the page, and nothing else changes.

## Names, only when you ask

By default no user, device, title or address is on the page. A metrics store that keeps names and titles is a viewing history, so it is yours to ask for:

| `EMBYFIN_METRICS_DETAIL` | Adds |
|---|---|
| `users` | Whose each stream is, time played and sign-ins by account, and when each was last seen. |
| `titles` | What each stream is playing, and time played by show and film. |
| `devices` | The device and app of each stream, and of each reason for a stream. |
| `all` | All three. |

Join words with commas: `users,titles`. A device's address is never on the page, whatever is asked. [More on names](docs/readings.md#names-only-when-you-ask).

## Configuration

Every setting is a flag, an environment variable or a line in a config file. Most setups need only these:

| Variable | What it is |
|---|---|
| `EMBYFIN_METRICS_EMBY_SERVER` | The Emby server's URL, e.g. `http://nas:8096`. |
| `EMBYFIN_METRICS_EMBY_TOKEN` | Its API key. |
| `EMBYFIN_METRICS_JELLYFIN_SERVER` | The Jellyfin server's URL, e.g. `http://nas:8097`. |
| `EMBYFIN_METRICS_JELLYFIN_TOKEN` | Its API key. |
| `EMBYFIN_METRICS_LISTEN` | Where the page is served. Default `:9096`. |
| `EMBYFIN_METRICS_DETAIL` | Who and what the page may name. Nobody by default. |

Leave a server out by setting neither its address nor its key. A key is never logged and never on the page.

[docs/configuration.md](docs/configuration.md) has every setting: the flags, the config file, how often each thing is read, labels of your own and the log level.

## How this is built

- The live tests start a real Emby and a real Jellyfin in Docker and check the page against them. They run on every change, and cover 55 of the 58 readings.
- The unit tests keep a saved copy of the whole page, so a renamed reading or label fails them.
- CI also runs golangci-lint, govulncheck, CodeQL and a Trivy scan of the built and the released programs.
- It lints the rest too: shell scripts, workflows (actionlint, zizmor, yamllint), spelling, markdown, and that the vendored dependencies match `go.mod`.

## More

- [docs/readings.md](docs/readings.md): every reading, its labels and what it means
- [docs/configuration.md](docs/configuration.md): every setting
- [docs/scraping.md](docs/scraping.md): Prometheus and Grafana Alloy configs, and queries
- [docs/development.md](docs/development.md): building, testing and the live tests
- [CONTRIBUTING.md](CONTRIBUTING.md): sending a change, and how releases are cut
- [CHANGELOG.md](CHANGELOG.md): what each version added
