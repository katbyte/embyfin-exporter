# Changelog

## 0.3.0 (2026-10-14)

Which plugins each server has, and which have a newer version waiting.

- **Plugins**: how many each server has, the version of each, and which have a newer version on offer. While a server cannot reach its catalogue, the page does not say how many are behind.

## 0.2.0 (2026-10-12)

What was played, who is streaming now and who signed in, with names only for whoever asks for them.

- **How much has been played**: the time and the plays of each week, by what kind of thing it was and how it reached the device. It is read from the Playback Reporting plugin, and the page says whether a server has it.
- **Who is streaming now**: a line for each stream, with how it is being sent, how fast, how far through it is and whether it is paused.
- **At home or away**: how many streams stay in the house and how many leave it, and what each lot comes to. No address is on the page.
- **Plays that ended within a minute**, which is what a play that failed looks like.
- **Why the server had to convert a stream, over the week** and not only this minute, on Emby.
- **Sign-ins and refused tries**, by whether they came from home or from outside.
- **What is new in each library** in the last day, 7 days and 30 days, on Emby.
- **Names only when you ask**: `EMBYFIN_METRICS_DETAIL` takes `users`, `titles`, `devices` or `all`, and puts who watched, what and on which device on the readings above, with when each account was last seen. Without it the page still names nobody.

## 0.1.2 (2026-10-12)

- **The bitrate of the streams a server is making is in bits a second**, as the servers and every player give one, where 0.1.0 gave it in bytes. The reading is `embyfin_transcode_bits_per_second`, in place of `embyfin_transcode_bytes_per_second`.

## 0.1.0 (2026-10-12)

The first release: a metrics page for an Emby and a Jellyfin media server.

- **How each server is doing, as a page Prometheus can read**: whether it is answering, its version, and whether a restart or an update is waiting.
- **What is playing**: how many devices are playing, paused or idle, and what kind of thing they are playing.
- **What playing it costs the server**: how many streams it has to convert for a device that cannot play the file as it is, why, how much they come to, and whether a graphics card is doing the work.
- **What the libraries hold**: films, episodes, albums, books and the rest, for each library and for the whole server.
- **Scheduled tasks**: how many are running, which failed last time, and how the library scan is getting on.
- **Accounts and devices**: how many there are, and how many were seen in the last day, week and month.
- **Nobody's name is on it**: no user, device, address or title, only counts.
- **One program for both servers**, or for either alone. It only reads from them, and one going down does not stop the other being reported.
- **`serve` keeps the page up** for a scraper to read, and `print` shows it once as a check that the servers can be reached.
- **Labels of your own on every reading**, to tell one setup from another.
- Ships as a Docker image, a Homebrew formula and ready-built programs.
