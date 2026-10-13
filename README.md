# embyfin-metrics - Emby and Jellyfin state as a Prometheus metrics page

[![GitHub release](https://img.shields.io/github/v/release/katbyte/embyfin-metrics?color=blueviolet)](https://github.com/katbyte/embyfin-metrics/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/embyfin-metrics?color=00ADD8)](https://github.com/katbyte/embyfin-metrics/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/embyfin-metrics?color=blue)](https://github.com/katbyte/embyfin-metrics/blob/main/LICENSE)
![build](https://github.com/katbyte/embyfin-metrics/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/embyfin-metrics/actions/workflows/pr-tests.yaml/badge.svg)
![acceptance](https://github.com/katbyte/embyfin-metrics/actions/workflows/pr-acceptance.yaml/badge.svg)
![lint](https://github.com/katbyte/embyfin-metrics/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/embyfin-metrics/badges/coverage.json)](https://github.com/katbyte/embyfin-metrics/actions/workflows/coverage.yaml)

One small program that reports the state of an [Emby](https://emby.media) and a [Jellyfin](https://jellyfin.org) server as a [Prometheus](https://prometheus.io) metrics page: how many libraries and what is in each, how many users, who is connected and how many of them are making the server transcode, whether a scan is running and how the last one went, and how much has been played.

One container covers both servers, or either alone. It only ever reads from them.

It is an exporter: it runs beside the servers it reports on, on the same host, and leaves saying which host that is to whatever scrapes it. It asks the servers over their own HTTP API, so nothing is installed in them, and it can be run somewhere else instead: `--labels` then puts a label of your choosing on every reading, to say whose they are.

## What is on the page

Every reading starts `embyfin_` and has a `server` label, `emby` or `jellyfin`.

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_up` | | 1 when the server answered the last time it was asked for its sessions, 0 when not |
| `embyfin_poll_duration_seconds` | `poll` | how long the last poll took; `poll` is `sessions`, `library` or `history` |
| `embyfin_poll_last_success_timestamp_seconds` | `poll` | when a poll was last answered in full, so stale readings can be told from fresh ones |
| `embyfin_poll_failures_total` | `poll` | polls that have failed since the program started |
| `embyfin_server_info` | `name`, `version`, `sdk_api_version` | always 1; the server's name and version, and the version of its API the client was built from |
| `embyfin_server_restart_pending` | | 1 when the server is waiting to be restarted |
| `embyfin_server_update_available` | | 1 when the server says a newer version of itself is out |
| `embyfin_sessions` | `state` | connected devices that are `playing`, `paused` or `idle` |
| `embyfin_streams` | `method` | sessions playing or paused, by how the file reaches the device: `direct_play` as it is, `direct_stream` repacked with nothing re-encoded, `transcode` with the picture or the sound re-encoded, or `unknown` |
| `embyfin_streams_by_type` | `type` | sessions playing or paused, by the kind of thing playing: `movie`, `episode`, `track`, `music_video`, `video`, `audiobook`, or `other` for anything else, live television and trailers included |
| `embyfin_video_transcodes` | `encoder` | sessions whose picture the server is re-encoding, the work that loads a CPU or a GPU, by whether `hardware` or `software` is encoding it |
| `embyfin_transcode_reasons` | `reason` | sessions the server is making a stream for, by each reason it gives for not sending the file as it is: `container_not_supported`, `subtitle_codec_not_supported` and so on, see [why a stream is made](#why-a-stream-is-made) |
| `embyfin_transcode_bits_per_second` | | the bitrate of the streams the server is making, added together, in bits a second |
| `embyfin_streams_by_network` | `network` | sessions playing or paused, by where the device is: `home` on the server's own network, `remote` from outside it, or `unknown` |
| `embyfin_streams_bits_per_second` | `network` | the bitrate of what is playing as it leaves the server, added together, by where the devices are |
| `embyfin_session_info` | `session`, `user`, `title`, `type`, `device`, `client`, `method`, `state`, `network`, `reasons` | always 1, one for each session playing or paused, see [who is streaming now](#who-is-streaming-now) |
| `embyfin_session_bits_per_second` | `session` | what each session plays comes to as it leaves the server: the stream the server makes, or the file's own when it is sent as it is |
| `embyfin_session_position_seconds` | `session` | how far through what it is playing each session is |
| `embyfin_session_runtime_seconds` | `session` | how long the whole of it is |
| `embyfin_client_transcode_reasons` | `device`, `client`, `reason` | sessions the server is making a stream for, by the device, its app and each reason; only when devices are named |
| `embyfin_scheduled_tasks` | `state` | the server's scheduled tasks that are `idle`, `running` or `cancelling` |
| `embyfin_scheduled_tasks_failed` | | scheduled tasks whose last run failed; one that was cancelled is not counted |
| `embyfin_scheduled_task_last_run_failed` | `task` | which they are: for each task that has run, 1 when its last run failed and 0 when not, under the name the server's own dashboard lists the task by |
| `embyfin_scheduled_task_last_run_timestamp_seconds` | `task` | when each task's last run ended |
| `embyfin_library_scan_running` | | 1 while the scan of every library runs |
| `embyfin_library_scan_progress_ratio` | | how far a running scan has got, from 0 to 1; on the page only while one runs |
| `embyfin_library_scan_last_run_ok` | | 1 when the last scan finished as it should, 0 when it failed or was cut short: Emby stops a scan under way when a library is added |
| `embyfin_library_scan_last_run_timestamp_seconds` | | when the last scan ended |
| `embyfin_library_scan_last_run_duration_seconds` | | how long the last scan took |
| `embyfin_libraries` | | how many libraries the server has |
| `embyfin_library_items` | `library`, `type` | items in each library by kind, see [what is counted](#what-is-counted) |
| `embyfin_items` | `type` | the same kinds with every library of the server added together |
| `embyfin_users` | | how many accounts the server has |
| `embyfin_users_disabled` | | how many of them have been disabled |
| `embyfin_users_active` | `window` | accounts seen within the last `1d`, `7d` or `30d` |
| `embyfin_user_last_seen_timestamp_seconds` | `user` | when each account last did anything; only when [accounts are named](#nobodys-name-is-on-it-unless-you-ask) |
| `embyfin_devices` | | how many devices the server knows of, which is every one it has ever seen |
| `embyfin_devices_active` | `window` | devices seen within the last `1d`, `7d` or `30d` |
| `embyfin_playback_reporting_installed` | | 1 when the server has the Playback Reporting plugin, which the readings below come from, and 0 when it has not, see [what was played](#what-was-played) |
| `embyfin_playback_seconds_total` | `week`, `type`, `method` | seconds played, by the kind of thing and how it reached the device |
| `embyfin_plays_total` | `week`, `type`, `method` | how many plays that was |
| `embyfin_user_playback_seconds_total` | `week`, `user`, `type` | seconds each account played, by the kind of thing; only when accounts are named |
| `embyfin_user_plays_total` | `week`, `user`, `type` | how many plays that was |
| `embyfin_title_playback_seconds_total` | `week`, `title`, `type` | seconds each show and each film was played; only when titles are named |
| `embyfin_title_plays_total` | `week`, `title`, `type` | how many plays that was |
| `embyfin_metrics_build_info` | `version`, `commit`, `goversion` | always 1; the version of embyfin-metrics that wrote the page (no `server` label) |

The page holds nothing but these: none of the Go runtime's or the process's own readings.

### What is counted

A library is counted for the kinds its type holds, so a film library is not asked for its albums:

| Library type | `type` |
|---|---|
| films | `movie` |
| TV | `series`, `season`, `episode` |
| music | `album`, `track`, and on Emby `artist` |
| music videos | `music_video` |
| collections | `collection` |
| playlists | `playlist` |
| books | `book`, and on Jellyfin `audiobook` |
| audiobooks (Emby) | `album`, `track`, which is how Emby keeps them |
| home videos and photos | `video`, `photo` |
| mixed, or anything else | `movie`, `series`, `season`, `episode`, `video` |

The counts are each server's own, so the two can differ over the same files: Emby counts every file of a film or an episode it holds in several versions, where Jellyfin counts the film once.

`embyfin_items` is the libraries added together and not the server's own total, which also counts what is in no library: the theme songs, trailers and extras kept beside a film, and albums no track belongs to any more.

A session's `method` goes by what the server is doing where it says: a stream it is making is a `direct_stream` when nothing in it is re-encoded and a `transcode` when anything is, whatever the device called it. Only with no stream being made is it the device's own word.

Two scheduled tasks of one name are one `task` on the page, failed if either did. A task goes by its name because Emby gives several tasks one key and some none.

### Why a stream is made

`embyfin_transcode_reasons` counts the streams a server is making, repacked or re-encoded, under each reason the server gives for not sending the file as it is. The reasons are the servers' own, some thirty of them, written in small letters: `ContainerNotSupported` is `container_not_supported`. A stream with two reasons is under both, so the reasons add up to more than the streams. There is no list of them to write at 0, so a reason is on the page only while a stream has it.

`embyfin_transcode_bits_per_second` is what the servers say those streams come to, added together, in bits a second as the servers and every player give a bitrate. A file sent as it is adds nothing to it.

### Who is streaming now

Each session that is playing or paused has a line of its own, `embyfin_session_info`, under the server's own id for the session. It is there while the session plays and gone when it stops, and one with nothing playing has none.

Left alone the line says what kind of thing is playing, how it reaches the device, whether it is paused and whether the device is at `home` or `remote`. Whose session it is (`user`), what it is playing (`title`), and on what (`device`, and `client` for the app) are empty unless [you ask for them](#nobodys-name-is-on-it-unless-you-ask). A title is written to be read: `Dark - S01E02 - Lies`, `Heat (1995)`, `Pink Floyd - Breathe`. `reasons` is why the server is making a stream of it, when it is, with commas between them.

The numbers are readings of their own under the same `session`, for a table to put beside the line:

```promql
embyfin_session_info                                                                   # who is streaming what, on what
embyfin_session_bits_per_second * on (server, session) group_left (user, title) embyfin_session_info
embyfin_session_position_seconds / on (server, session) embyfin_session_runtime_seconds   # how far through, from 0 to 1
```

**Home or remote is worked out from the address a session connects from**, and the address itself is never on the page: an address that only works on a private network, or on the server's own machine, is `home`, and any other is `remote`. A server behind a proxy that does not pass the real address on sees every device at the proxy's address, and so at home.

**`embyfin_streams_bits_per_second{network="remote"}` is what is going out of the house**: for each stream, what the server is making for it, or what the file itself comes to when it is sent as it is.

### What was played

Neither server keeps a record of how long anything was played. The [Playback Reporting](https://github.com/faush01/playback_reporting) plugin does, on Emby and on [Jellyfin](https://github.com/jellyfin/jellyfin-plugin-playbackreporting), and the readings of what was played are its sums. A server without the plugin has `embyfin_playback_reporting_installed` at 0 and none of them; nothing else on the page changes.

Each is a counter, for a query to take the `increase()` of over a day or any other stretch:

```promql
sum(increase(embyfin_playback_seconds_total[1d])) / 3600                           # hours played in the last day
sum by (user) (increase(embyfin_user_playback_seconds_total[1d])) / 60             # minutes each account played
topk(10, sum by (title) (increase(embyfin_title_playback_seconds_total[7d])))      # the ten most watched of the week
```

**Add the weeks together.** Plays are kept under the week they began in, named by its Monday as the server's own clock has it, and the page carries this week and the last. So a reading never goes down and never starts again: a new week is a new reading, and a film begun late on Sunday goes on adding to the week it began in. Always `sum` the `week` label away, as the queries above do, and a stretch that runs across a week's end, a month's end or a restart of this program comes out right.

**Time is counted as it is played**, not when the play ends: the plugin adds to a play's length every few seconds while it runs, so the readings can also say when in the day people watch.

**After a restart the readings come back as they were**, since they are the plugin's sums and not this program's own count, and anything played while it was stopped is there when it returns. What was played before the program was first scraped is not: the store only has what it was given, and the plugin keeps the older days.

**A play is one line of the plugin's record**, which is what its own Play Count counts: a play that changes how it reaches the device part way is two.

**`method` is as for a session**: a stream the server made with nothing re-encoded is a `direct_stream`. `type` is the kinds a session has.

**A title is a show or a film.** An episode is under its show, and there is no reading for each song or trailer. **An account is under its name**, and plays by accounts since deleted are together under `unknown`.

The plugin is asked with a `POST`, which is the only way it takes a question, and it would run whatever it was sent. This program sends it one question, written in the program and asking for sums; it is still only a read.

### Nobody's name is on it unless you ask

Left alone there is no user name, device name, client address or title of what is playing anywhere on the page, only counts and each session's state. The nearest it comes is the kind of thing playing, a film or an episode, and the names of the server's own scheduled tasks. A metrics store that keeps names and titles is a viewing history, so it is yours to ask for:

| `EMBYFIN_METRICS_DETAIL` | Adds |
|---|---|
| `users` | whose each session is, the time each account has played, and when it was last seen |
| `titles` | what each session is playing, and the time each show and each film has been played |
| `devices` | the device and the app each session plays on, and which of them each reason for a stream belongs to |
| `all` | all three, and whatever a later version can name |

Words are joined with commas: `users,titles`. What a word left out would name is left behind in the server's answer, or never asked of the server at all, so it cannot reach a label by accident. The address a device connects from is never on the page whatever is asked.

### When a server stops answering

`embyfin_up` goes to 0 at the next poll, and the other server's readings carry on. The stopped server's last readings stay on the page for three polls, to ride out a blip, and are then left off rather than shown as if they were current: three polls is 45 seconds for sessions, 90 seconds for what was played and 15 minutes for libraries, at the default intervals. A reading that has never been answered is absent, not zero.

What was played is asked of a plugin and not of the server itself, so it has a poll of its own: a plugin that fails shows as `embyfin_poll_failures_total{poll="history"}` going up, and the server is still up.

## Installation

```bash
docker pull ghcr.io/katbyte/embyfin-metrics:latest
```

or

```bash
go install github.com/katbyte/embyfin-metrics@latest
```

or

```bash
brew install katbyte/tap/embyfin-metrics
```

## Configuration

All options can be passed as command-line flags, environment variables, or via a configuration file.

| Variable | Flag | Description |
|---|---|---|
| `EMBYFIN_METRICS_EMBY_SERVER` | `--emby-server` | the Emby server's URL, e.g. `http://nas:8096` |
| `EMBYFIN_METRICS_EMBY_TOKEN` | `--emby-token` | its API key (dashboard → Advanced → API Keys) |
| `EMBYFIN_METRICS_JELLYFIN_SERVER` | `--jellyfin-server` | the Jellyfin server's URL, e.g. `http://nas:8097` |
| `EMBYFIN_METRICS_JELLYFIN_TOKEN` | `--jellyfin-token` | its API key (dashboard → API Keys) |
| `EMBYFIN_METRICS_LISTEN` | `--listen` | the address the page is served on, default `:9096` |
| `EMBYFIN_METRICS_LABELS` | `--labels` | labels to put on every reading, as `name=value,name=value`, e.g. `hostname=nas`; none by default. A label named as one a reading already has, `server` say, stops the program with its name |
| `EMBYFIN_METRICS_DETAIL` | `--detail` | who and what the page may name, as a list of `users`, `titles`, `devices` or `all`; nobody and nothing by default, see [nobody's name is on it unless you ask](#nobodys-name-is-on-it-unless-you-ask). A word it does not know stops the program |
| `EMBYFIN_METRICS_SESSIONS_INTERVAL` | `--sessions-interval` | how often sessions, streams, scheduled tasks and the server's own state are read, default `15s` |
| `EMBYFIN_METRICS_LIBRARY_INTERVAL` | `--library-interval` | how often libraries, item counts, users and devices are read, default `5m` |
| `EMBYFIN_METRICS_HISTORY_INTERVAL` | `--history-interval` | how often what was played is read, from a server with the Playback Reporting plugin, default `30s` |
| `EMBYFIN_METRICS_LOG` | | log level (`WARN` default): `INFO` adds what is being reported on and when a server comes back, `TRACE` every request to a server and its answer, with keys blanked. An answer names users and what they are playing, so `TRACE` is for finding a fault and not for leaving on |
| `EMBYFIN_METRICS_CONFIG` | `--config` | read settings from this file and no other, in place of `./.embyfin-metrics` or `~/.embyfin-metrics` |

Either server can be left out by setting neither its address nor its key. An address with no key, or a key with no address, stops the program with the name of the variable that is missing.

A key is never logged and never on the page.

### Configuration File

You can place a `.embyfin-metrics` file in your home directory `~/.embyfin-metrics` or in your current directory `./.embyfin-metrics`. Keys match the long flag names using the `env` format, with an underscore between words:

```env
EMBY_SERVER=http://nas:8096
EMBY_TOKEN=...
JELLYFIN_SERVER=http://nas:8097
JELLYFIN_TOKEN=...
```

A flag or an environment variable still wins over the file. `--config` (or `EMBYFIN_METRICS_CONFIG`) names the file to read, and it is then the only one read; a file that is named and is not there stops the program from starting.

## Usage

```bash
embyfin-metrics print   # ask each server once and print the page: a check that the addresses and keys work
embyfin-metrics serve   # serve the page at /metrics, with a health probe at /healthz
```

### Docker

[`docker-compose.yml`](docker-compose.yml) runs the released image beside the servers, with the two keys in a `.env` file next to it (see [`.env.example`](.env.example)):

```bash
cp .env.example .env   # then put the two API keys in it
docker compose up -d
curl -s http://127.0.0.1:9096/metrics
```

The example publishes the port on `127.0.0.1` only, since the page needs no password and whatever scrapes it usually runs on the same host.

### Scraping it

Prometheus:

```yaml
scrape_configs:
  - job_name: embyfin
    scrape_interval: 30s
    static_configs:
      - targets: ['127.0.0.1:9096']
```

Grafana Alloy:

```alloy
prometheus.scrape "embyfin" {
  targets         = [{"__address__" = "127.0.0.1:9096"}]
  job_name        = "embyfin"
  scrape_interval = "30s"
  forward_to      = [prometheus.remote_write.default.receiver]
}
```

A scrape is answered from memory and never waits on a server: the servers are asked on their own schedule, and the page holds the last answers. With the default 15 second sessions interval, a scrape every 30 seconds never reads a session count older than 15 seconds.

A few things to ask of it:

```promql
embyfin_up == 0                                                    # a server is down
sum by (server) (embyfin_streams{method="transcode"})              # streams being transcoded
sum by (server) (embyfin_video_transcodes{encoder="software"})     # pictures re-encoded on the CPU
time() - embyfin_poll_last_success_timestamp_seconds > 900         # readings gone stale
embyfin_library_scan_last_run_ok == 0                              # the last library scan failed or was cut short
embyfin_scheduled_task_last_run_failed == 1                        # which scheduled task's last run failed
sum by (server, reason) (embyfin_transcode_reasons)                # why streams are being made
embyfin_transcode_bits_per_second / 1e6                            # what the server's own streams come to, in megabits a second
sum by (server) (increase(embyfin_playback_seconds_total[1d]))     # seconds played in the last day
embyfin_streams_bits_per_second{network="remote"} / 1e6            # megabits a second going out of the house
```

## Development

```bash
make            # fmt + build
make test       # the tests, under the race detector
make testacc    # the live tests, against a throwaway Emby and Jellyfin in Docker
make check-all  # build + tests (incl. live) + every linter + depscheck
```

The servers are spoken to with [embyfin-mcp](https://github.com/katbyte/embyfin-mcp)'s client, which hides most of what differs between the two; what is left is in `lib/mediaserver`, next to a note of the server and version it was seen on.

The tests under `make test` need no Emby or Jellyfin: each runs against a server the test makes up (`lib/poll/polltest`). The whole page is kept in [`lib/readings/testdata/page.txt`](lib/readings/testdata/page.txt), so a reading that is renamed shows up as a difference there; `go test ./lib/readings -update` rewrites it once the change is meant.

The page itself, its `/metrics` and `/healthz` routes and the labels are [go-kt](https://github.com/katbyte/go-kt)'s `pmetrics`, shared with the other tools that serve one; what is here is the readings (`lib/readings`) and the asking (`lib/poll`, `lib/mediaserver`).

The live tests (`acceptance/`) are where a real server gets to answer otherwise. [`.scripts/testenv.sh`](.scripts/testenv.sh) starts an Emby or a Jellyfin in Docker with no way out to the internet, lays out a small made-up catalogue for it, gives it the Playback Reporting plugin, gets it through its setup and mints a key. The catalogue has something of every kind the page counts: films, series, music, music videos, a home video and photos, books, an audiobook, and a folder that mixes a film with a series. The suite then checks the page against the server each time it changes something:

- it makes a library of each type, a collection and a playlist, and checks every count
- it signs a device in and plays, pauses and stops a film, then plays one thing of each kind
- it has the server re-encode a film for a device, and only repack one for another
- it checks the line each playing session has of its own: whose it is, the film, the device and its app for a program told to name them, none of them for one that was not, and gone once stopped
- it asks for a scan and catches it under way
- it plays a film and watches the time played go up on the page while it plays, under the account and the title only for a program told to name them
- it lays plays down in the plugin's record in this week, the last and three weeks back, and checks each is under its own week or off the page
- it runs `serve` and scrapes it over HTTP, then freezes the server's container to see it reported as down, its readings left off, and back again when thawed
- it starts the image `make docker` builds beside the server, and checks it serves the page, passes its own health check, does not run as root, and names accounts when its environment asks

It needs `docker`, `ffmpeg`, `jq` and `unzip`, and takes about two minutes for both servers once the images are there.

```bash
make testacc BACKENDS=jellyfin                                             # one server only
eval "$(EMBYFIN_METRICS_TEST_BACKEND=emby .scripts/testenv.sh up)"         # or keep one up and run against it as often as needed
make test-live
EMBYFIN_METRICS_TEST_BACKEND=emby .scripts/testenv.sh down
```

Run by hand like that, the test of the image is skipped unless `EMBYFIN_METRICS_TEST_PROGRAM_IMAGE` names one; `make testacc` builds it and does.

What the live tests cannot reach stays with the unit tests: a transcode on a GPU, which the containers do not have, a scheduled task that fails, which none does on a server left to itself, and live television.

Dependencies are vendored, and the dev tools are pinned in `.tools/go.mod` and the makefile; `make tools` installs them into `.tools/bin`.
