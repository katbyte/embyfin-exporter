# Changelog

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
