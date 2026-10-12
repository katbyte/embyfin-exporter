# Changelog

## Unreleased

### Added

- `serve`: asks an Emby and a Jellyfin server for their state on a schedule and serves the last answers at `/metrics` in the Prometheus text format, with a health probe at `/healthz`. One process covers both servers or either alone, and only ever reads from them
- `print`: asks each server once and prints the page, as a check that the addresses and keys work
- the readings, each with a `server` label: whether the server is up, its name and version, whether a restart or an update is pending, libraries and the items in each by kind, the same with every library added together, users, how many are disabled and how many were seen within a day, 7 days and 30 days, devices and how many were seen within the same windows, sessions by state, streams by play method and by the kind of thing playing, video transcodes by hardware or software, scheduled tasks by state and how many last failed, and whether the library scan is running, how far it has got, how its last run went, when it ended and how long it took
- each scheduled task that has run is on the page by name, with whether its last run failed and when it ended, so a count of failed tasks can be traced to the one
- why a server is making each stream it makes, counted by the server's own reasons, and how many bytes a second those streams come to between them
- a library is asked only for the kinds its type holds, and the count for a whole server is its libraries added together, so the theme songs, trailers and extras a server keeps beside a film are not counted as tracks and videos
- a stream is called a transcode when the server is re-encoding its picture or its sound, and a direct stream when it is only repacking it, whatever the device playing it calls it
- how the polling itself is going: how long each poll took, when each was last answered in full, and how many have failed
- no user name, device name, client address or title is on the page, only counts
- a scrape is answered from memory and never waits on a server. Sessions, streams, scheduled tasks and the server's own state are read every 15 seconds and libraries, item counts, users and devices every 5 minutes; both are settings
- one server stopping does not stop the other being reported, and a stopped server's last readings are left off the page after three polls rather than shown as current
- `--labels` (`EMBYFIN_METRICS_LABELS`) puts labels of your choosing on every reading, for when the page is scraped from somewhere that cannot tell whose it is
- every setting is a flag, an `EMBYFIN_METRICS_*` environment variable, or a line in a `.embyfin-metrics` file
- live tests against a throwaway Emby and a throwaway Jellyfin in Docker, each holding a small made-up catalogue: `make testacc`
- the servers are spoken to with the client from [embyfin-mcp](https://github.com/katbyte/embyfin-mcp), built on Emby's 4.10 and Jellyfin's 12.1 API
- an Alpine Docker image at `ghcr.io/katbyte/embyfin-metrics`, serving on port 9096, plus `docker-compose.yml`
