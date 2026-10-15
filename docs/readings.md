# What is on the page

Every reading starts `embyfin_` and has a `server` label, `emby` or `jellyfin`. The page holds nothing but these: none of the Go runtime's or the process's own readings.

- [The server](#the-server)
- [Sessions and streams](#sessions-and-streams)
- [Why a stream is made](#why-a-stream-is-made)
- [Who is streaming now](#who-is-streaming-now)
- [Scheduled tasks and scans](#scheduled-tasks-and-scans)
- [Plugins](#plugins)
- [Libraries](#libraries)
- [Accounts and devices](#accounts-and-devices)
- [Sign-ins](#sign-ins)
- [What was played](#what-was-played)
- [Names, only when you ask](#names-only-when-you-ask)

## The server

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_up` | | 1 when the server answered the last time it was asked for its sessions, 0 when not. |
| `embyfin_poll_duration_seconds` | `poll` | How long the last poll took. `poll` is `sessions`, `library` or `history`. |
| `embyfin_poll_last_success_timestamp_seconds` | `poll` | When a poll was last answered in full, so stale readings can be told from fresh ones. |
| `embyfin_poll_failures_total` | `poll` | Polls that have failed since the program started. |
| `embyfin_server_info` | `name`, `version`, `sdk_api_version` | Always 1. The server's name and version, and the version of its API the client was built from. |
| `embyfin_server_restart_pending` | | 1 when the server is waiting to be restarted. |
| `embyfin_server_update_available` | | 1 when the server says a newer version of itself is out. |
| `embyfin_metrics_build_info` | `version`, `commit`, `goversion` | Always 1. The version of embyfin-exporter that wrote the page. It has no `server` label. |

### When a server stops answering

`embyfin_up` goes to 0 at the next poll. The other server's readings carry on.

The stopped server's last readings stay on the page for three polls, to ride out a blip. After that they are left off rather than shown as if they were current. At the default intervals, three polls is 45 seconds for sessions, 90 seconds for what was played and 15 minutes for libraries. A reading that has never been answered is absent, not zero.

What was played is asked of a plugin and not of the server itself, so it has a poll of its own. A plugin that fails shows as `embyfin_poll_failures_total{poll="history"}` going up, and the server is still up.

## Sessions and streams

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_sessions` | `state` | Connected devices that are `playing`, `paused` or `idle`. |
| `embyfin_streams` | `method` | Sessions playing or paused, by how the file reaches the device. |
| `embyfin_streams_by_type` | `type` | Sessions playing or paused, by the kind of thing playing. |
| `embyfin_video_transcodes` | `encoder` | Sessions whose picture the server is re-encoding, by whether `hardware` or `software` is encoding it. |
| `embyfin_streams_by_network` | `network` | Sessions playing or paused, by where the device is: `home`, `remote` or `unknown`. |
| `embyfin_streams_bits_per_second` | `network` | The bitrate of what is playing as it leaves the server, added together, by where the devices are. |

`method` is how the file reaches the device:

- `direct_play`: as it is
- `direct_stream`: repacked, with nothing re-encoded
- `transcode`: with the picture or the sound re-encoded
- `unknown`

A session's `method` goes by what the server is doing, where it says. A stream it is making is a `direct_stream` when nothing in it is re-encoded and a `transcode` when anything is, whatever the device called it. Only with no stream being made is it the device's own word.

`type` is `movie`, `episode`, `track`, `music_video`, `video`, `audiobook` or `other`. Anything else is `other`, live television and trailers included.

Re-encoding the picture is the work that loads a CPU or a GPU, which is what `embyfin_video_transcodes` counts.

### Home or remote

Home or remote is worked out from the address a session connects from. The address itself is never on the page. An address that only works on a private network, or on the server's own machine, is `home`. Any other is `remote`.

A server behind a proxy that does not pass the real address on sees every device at the proxy's address, and so at home.

`embyfin_streams_bits_per_second{network="remote"}` is what is going out of the house. For each stream that is what the server is making for it, or what the file itself comes to when it is sent as it is.

## Why a stream is made

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_transcode_reasons` | `reason` | Sessions the server is making a stream for, by each reason it gives for not sending the file as it is. |
| `embyfin_transcode_bits_per_second` | | The bitrate of the streams the server is making, added together, in bits a second. |
| `embyfin_client_transcode_reasons` | `device`, `client`, `reason` | The same sessions by the device, its app and each reason. Only when devices are named. |

`embyfin_transcode_reasons` counts the streams a server is making, repacked or re-encoded. Each is under every reason the server gives for not sending the file as it is.

- The reasons are the servers' own, some thirty of them, written in small letters: `ContainerNotSupported` is `container_not_supported`.
- A stream with two reasons is under both, so the reasons add up to more than the streams.
- There is no list of them to write at 0, so a reason is on the page only while a stream has it.

`embyfin_transcode_bits_per_second` is what the servers say those streams come to, added together. It is in bits a second, as the servers and every player give a bitrate. A file sent as it is adds nothing to it.

For the reasons over a week and not only this minute, see [what was played](#what-was-played).

## Who is streaming now

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_session_info` | `session`, `user`, `title`, `type`, `device`, `client`, `method`, `state`, `network`, `reasons` | Always 1, one for each session playing or paused. |
| `embyfin_session_bits_per_second` | `session` | What the session comes to as it leaves the server: the stream the server makes, or the file's own when sent as it is. |
| `embyfin_session_position_seconds` | `session` | How far through what it is playing the session is. |
| `embyfin_session_runtime_seconds` | `session` | How long the whole of it is. |

Each session that is playing or paused has a line of its own, `embyfin_session_info`, under the server's own id for the session. It is there while the session plays and gone when it stops. A session with nothing playing has none.

Left alone, the line says what kind of thing is playing, how it reaches the device, whether it is paused, and whether the device is at [`home` or `remote`](#home-or-remote). These labels are empty unless [you ask for them](#names-only-when-you-ask):

- `user`: whose session it is
- `title`: what it is playing, written to be read: `Dark - S01E02 - Lies`, `Heat (1995)`, `Pink Floyd - Breathe`
- `device` and `client`: the device and its app

`reasons` is why the server is making a stream of it, when it is, with commas between them.

The numbers are readings of their own under the same `session`, for a table to put beside the line:

```promql
embyfin_session_info                                                                   # who is streaming what, on what
embyfin_session_bits_per_second * on (server, session) group_left (user, title) embyfin_session_info
embyfin_session_position_seconds / on (server, session) embyfin_session_runtime_seconds   # how far through, from 0 to 1
```

## Scheduled tasks and scans

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_scheduled_tasks` | `state` | The server's scheduled tasks that are `idle`, `running` or `cancelling`. |
| `embyfin_scheduled_tasks_failed` | | Scheduled tasks whose last run failed. One that was cancelled is not counted. |
| `embyfin_scheduled_task_last_run_failed` | `task` | For each task that has run, 1 when its last run failed and 0 when not. |
| `embyfin_scheduled_task_last_run_timestamp_seconds` | `task` | When each task's last run ended. |
| `embyfin_library_scan_running` | | 1 while the scan of every library runs. |
| `embyfin_library_scan_progress_ratio` | | How far a running scan has got, from 0 to 1. On the page only while one runs. |
| `embyfin_library_scan_last_run_ok` | | 1 when the last scan finished as it should, 0 when it failed or was cut short. |
| `embyfin_library_scan_last_run_timestamp_seconds` | | When the last scan ended. |
| `embyfin_library_scan_last_run_duration_seconds` | | How long the last scan took. |

`task` is the name the server's own dashboard lists the task by. A task goes by its name because Emby gives several tasks one key and some none. Two scheduled tasks of one name are one `task` on the page, failed if either did.

Emby stops a scan under way when a library is added, which shows as a last scan that was cut short.

## Plugins

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_plugins` | | How many plugins the server has installed. |
| `embyfin_plugin_info` | `plugin`, `version` | Always 1, one for each plugin installed, with its version. |
| `embyfin_plugins_update_available` | | Plugins with a newer version on offer than the one installed. |
| `embyfin_plugin_update_available` | `plugin`, `version`, `latest` | Always 1, one for each such plugin, with the version installed and the newest on offer. |

A plugin is behind when the server's own catalogue has a newer version of it that the server could run. That is the choice each server makes for itself:

- Emby leaves out a version less settled than the server is set to take, a beta say, and one that needs a newer server.
- Jellyfin leaves out a version built for a newer server.

```promql
embyfin_plugins_update_available > 0   # a server has plugins behind
embyfin_plugin_update_available        # which: the version installed and the newest on offer
```

Both servers fetch a plugin's update themselves, when they start and on a timer: every 3 hours on Emby and every day on Jellyfin, as they come. What then waits is a restart, which `embyfin_server_restart_pending` says. The two differ in what they list meanwhile:

- Emby goes on listing the version that is running, so the plugin stays behind until the restart.
- Jellyfin lists the new version beside the old, so nothing is behind once it is fetched.

So a plugin behind for more than a day means the server is not fetching updates, or is waiting on a restart.

The count is not on the page while a server cannot reach its catalogue. The plugins are still listed. How many are behind is then not known, which is not the same as none.

The catalogue is read once an hour, since it changes rarely and Emby's is over a megabyte. A plugin's name is always on the page: it names no person.

## Libraries

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_libraries` | | How many libraries the server has. |
| `embyfin_library_items` | `library`, `type` | Items in each library by kind. |
| `embyfin_items` | `type` | The same kinds with every library of the server added together. |
| `embyfin_library_items_added` | `library`, `window` | Items added to each library within the last `1d`, `7d` or `30d`. Emby only. |

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

The counts are each server's own, so the two can differ over the same files. Emby counts every file of a film or an episode it holds in several versions. Jellyfin counts the film once.

`embyfin_items` is the libraries added together and not the server's own total. The server's total also counts what is in no library: the theme songs, trailers and extras kept beside a film, and albums no track belongs to any more.

### What is new

`embyfin_library_items_added` is how many of a library's items were added within the last day, 7 days and 30 days. It counts films, episodes, tracks, books and the like, and not the shows, seasons and albums that hold them. Read at the end of each day, the `1d` window is what was added that day.

Only Emby can count them: it is asked for the count and hands over no items. Jellyfin has no such question, and reading its newest items to count them takes over a second a thousand on a large library. So a Jellyfin's page says nothing of what is new.

For Jellyfin, and for what has gone as well as what has come, `delta(embyfin_library_items[1d])` is how much each library grew.

## Accounts and devices

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_users` | | How many accounts the server has. |
| `embyfin_users_disabled` | | How many of them have been disabled. |
| `embyfin_users_active` | `window` | Accounts seen within the last `1d`, `7d` or `30d`. |
| `embyfin_user_last_seen_timestamp_seconds` | `user` | When each account last did anything. Only when accounts are named. |
| `embyfin_devices` | | How many devices the server knows of, which is every one it has ever seen. |
| `embyfin_devices_active` | `window` | Devices seen within the last `1d`, `7d` or `30d`. |

## Sign-ins

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_logins_total` | `week`, `result`, `network` | Times an account was signed in to (`ok`) and tries that were refused (`failed`), by where they came from. |
| `embyfin_user_logins_total` | `week`, `user`, `result` | The same by account. Only when accounts are named. |

`embyfin_logins_total` is read from each server's activity log. Where a sign-in came from is `home`, `remote` or `unknown`, worked out from the address the server noted and never the address itself. On a server the internet can reach, the one to watch is:

```promql
sum by (server) (increase(embyfin_logins_total{result="failed",network="remote"}[1h]))
```

As with what was played, a sign-in is kept under the week it was made in, with this week and the last on the page. Here the week is named by its Monday in UTC. `sum` the `week` away.

With accounts named, `embyfin_user_logins_total` has the same by account:

- The account is read out of the server's own sentence for the sign-in, in the English both servers write by default. A server set to another language has them under `unknown`.
- A try on a name the server has no account of is under `unknown` too, whatever was typed. Nothing someone outside types becomes a label.
- Emby notes such tries and Jellyfin does not.

Neither server keeps a note of a play that failed in its activity log, so there is no reading of playback errors from it. The nearest is `direct_play_error`, under [what was played](#what-was-played).

## What was played

| Reading | Labels | What it says |
|---|---|---|
| `embyfin_playback_reporting_installed` | | 1 when the server has the Playback Reporting plugin, which the readings below come from, and 0 when not. |
| `embyfin_playback_seconds_total` | `week`, `type`, `method` | Seconds played, by the kind of thing and how it reached the device. |
| `embyfin_plays_total` | `week`, `type`, `method` | How many plays that was. |
| `embyfin_user_playback_seconds_total` | `week`, `user`, `type` | Seconds each account played, by the kind of thing. Only when accounts are named. |
| `embyfin_user_plays_total` | `week`, `user`, `type` | How many plays that was. |
| `embyfin_title_playback_seconds_total` | `week`, `title`, `type` | Seconds each show and each film was played. Only when titles are named. |
| `embyfin_title_plays_total` | `week`, `title`, `type` | How many plays that was. |
| `embyfin_short_plays_total` | `week`, `type`, `method` | Plays that ended within a minute of beginning, which is what one that failed looks like. |
| `embyfin_transcode_reason_seconds_total` | `week`, `reason` | Seconds played of streams the server made, by each reason it gave. Emby only. |
| `embyfin_transcode_reason_plays_total` | `week`, `reason` | How many plays that was. |
| `embyfin_client_transcode_reason_seconds_total` | `week`, `device`, `client`, `reason` | The same by the device and its app. Emby only, and only when devices are named. |
| `embyfin_client_transcode_reason_plays_total` | `week`, `device`, `client`, `reason` | How many plays that was. |

Neither server keeps a record of how long anything was played. The [Playback Reporting](https://github.com/faush01/playback_reporting) plugin does, on Emby and on [Jellyfin](https://github.com/jellyfin/jellyfin-plugin-playbackreporting), and these readings are its sums. A server without the plugin has `embyfin_playback_reporting_installed` at 0 and none of them. Nothing else on the page changes.

Each is a counter, for a query to take the `increase()` of over a day or any other stretch:

```promql
sum(increase(embyfin_playback_seconds_total[1d])) / 3600                           # hours played in the last day
sum by (user) (increase(embyfin_user_playback_seconds_total[1d])) / 60             # minutes each account played
topk(10, sum by (title) (increase(embyfin_title_playback_seconds_total[7d])))      # the ten most watched of the week
```

### Add the weeks together

Plays are kept under the week they began in. A week is named by its Monday, as the server's own clock has it, and the page carries this week and the last.

So a reading never goes down and never starts again. A new week is a new reading, and a film begun late on Sunday goes on adding to the week it began in.

Always `sum` the `week` label away, as the queries above do. A stretch that runs across a week's end, a month's end or a restart of this program then comes out right.

### How plays are counted

- **Time is counted as it is played**, not when the play ends. The plugin adds to a play's length every few seconds while it runs, so the readings can also say when in the day people watch.
- **After a restart the readings come back as they were.** They are the plugin's sums and not this program's own count, so anything played while it was stopped is there when it returns.
- **What was played before the program was first scraped is not there.** The store only has what it was given. The plugin keeps the older days.
- **A play is one line of the plugin's record**, which is what its own Play Count counts. A play that changes how it reaches the device part way is two.
- **`method` is as for a session.** A stream the server made with nothing re-encoded is a `direct_stream`. `type` is the kinds a session has.
- **A title is a show or a film.** An episode is under its show, and there is no reading for each song or trailer.
- **An account is under its name.** Plays by accounts since deleted are together under `unknown`.

### Short plays

A short play is one that ended within a minute. It is counted five minutes after it stopped, since a play that has just begun is short too.

It is what a play that failed looks like. It is also what skipping through songs looks like, so read it by `type` and `method`.

The two plugins differ:

- Emby's starts a new line each time a play changes how it reaches the device. A file that will not play as it is, and is then made a stream of, shows as a short `direct_play`.
- Jellyfin's does not record a play that lasts under 20 seconds at all.

### Why streams were made, over the week

Only Emby's plugin keeps the reasons. On Emby the page has the plays and the seconds under each reason the server gave. With devices named it has them by device and app too. So the television or the app that needs a setting changed shows up over a week, and not only while it plays.

`direct_play_error` is the nearest either server comes to recording a play that went wrong: the device tried the file as it is and could not play it.

Jellyfin's plugin keeps no reasons. There the reasons are only on the page [while a stream is being made](#why-a-stream-is-made).

### It is still only a read

The plugin is asked with a `POST`, which is the only way it takes a question, and it would run whatever it was sent. This program sends it one question, written in the program and asking for sums.

## Names, only when you ask

Left alone, there is no user name, device name, client address or title of what is playing anywhere on the page. There are only counts and each session's state. The nearest it comes is the kind of thing playing, a film or an episode, and the names of the server's own scheduled tasks and plugins.

A metrics store that keeps names and titles is a viewing history, so it is yours to ask for:

| `EMBYFIN_EXPORTER_DETAIL` | Adds |
|---|---|
| `users` | Whose each session is, the time each account has played, when it was last seen, and its sign-ins. |
| `titles` | What each session is playing, and the time each show and each film has been played. |
| `devices` | The device and the app each session plays on, and which of them each reason for a stream belongs to, now and over the week. |
| `all` | All three, and whatever a later version can name. |

Words are joined with commas: `users,titles`. A word the program does not know stops it.

What a word left out would name is left behind in the server's answer, or never asked of the server at all. So it cannot reach a label by accident.

The address a device connects from is never on the page, whatever is asked.
