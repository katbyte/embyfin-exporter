# Development

```bash
make            # fmt + build
make test       # the tests, under the race detector
make testacc    # the live tests, against a throwaway Emby and Jellyfin in Docker
make check-all  # build + tests (incl. live) + every linter + depscheck
```

Dependencies are vendored. The dev tools are pinned in `.tools/go.mod` and the makefile, and `make tools` installs them into `.tools/bin`.

## Where things are

- `lib/poll` asks the servers on a schedule and keeps the last answers.
- `lib/mediaserver` is what differs between the two servers, each next to a note of the server and version it was seen on.
- `lib/readings` turns the answers into the page.
- `cli` is the commands and the settings.

The servers are spoken to with [embyfin-mcp](https://github.com/katbyte/embyfin-mcp)'s client, which hides most of what differs between the two.

The page itself, its `/metrics` and `/healthz` routes and the labels are [go-kt](https://github.com/katbyte/go-kt)'s `pmetrics`, shared with the other tools that serve one.

## Unit tests

The tests under `make test` need no Emby or Jellyfin. Each runs against a server the test makes up (`lib/poll/polltest`).

The whole page is kept in [`lib/readings/testdata/page.txt`](../lib/readings/testdata/page.txt), so a reading that is renamed shows up as a difference there. `go test ./lib/readings -update` rewrites it once the change is meant.

## Live tests

The live tests (`acceptance/`) are where a real server gets to answer otherwise. [`.scripts/testenv.sh`](../.scripts/testenv.sh) sets one up:

- it starts an Emby or a Jellyfin in Docker with no way out to the internet
- it lays out a small made-up catalogue for it
- it gives it the Playback Reporting plugin
- it gets it through its setup and mints a key

The catalogue has something of every kind the page counts: films, series, music, music videos, a home video and photos, books, an audiobook, and a folder that mixes a film with a series.

The suite then checks the page against the server each time it changes something:

- it makes a library of each type, a collection and a playlist, and checks every count, and on Emby that all of it is counted as new
- it signs a device in and plays, pauses and stops a film, then plays one thing of each kind
- it has the server re-encode a film for a device, and only repack one for another
- it checks the line each playing session has of its own: whose it is, the film, the device and its app for a program told to name them, and none of them for one that was not
- it checks the line is gone once the session stops
- it asks for a scan and catches it under way
- it checks the page lists the plugins the server lists, each at its version, and does not say which are behind while the server cannot reach its catalogue
- it plays a film and watches the time played go up on the page while it plays, under the account and the title only for a program told to name them
- it lays plays down in the plugin's record in this week, the last and three weeks back, and checks each is under its own week or off the page
- it checks the short ones are counted and, on Emby, that each is under the reason and the device it was made a stream for
- it signs in rightly, wrongly and as nobody the server knows, and checks each is counted and that the name tried is on no page
- it runs `serve` and scrapes it over HTTP, then freezes the server's container to see it reported as down, its readings left off, and back again when thawed
- it starts the image `make docker` builds beside the server, and checks it serves the page, passes its own health check, does not run as root, and names accounts when its environment asks

It needs `docker`, `ffmpeg`, `jq` and `unzip`. It takes about four minutes for both servers once the images are there.

```bash
make testacc BACKENDS=jellyfin                                             # one server only
eval "$(EMBYFIN_METRICS_TEST_BACKEND=emby .scripts/testenv.sh up)"         # or keep one up and run against it as often as needed
make test-live
EMBYFIN_METRICS_TEST_BACKEND=emby .scripts/testenv.sh down
```

Run by hand like that, the test of the image is skipped unless `EMBYFIN_METRICS_TEST_PROGRAM_IMAGE` names one. `make testacc` builds it and does.

## What the live tests cannot reach

These stay with the unit tests:

- a transcode on a GPU, which the containers do not have
- a scheduled task that fails, which none does on a server left to itself
- live television
- which plugins are behind, since the test server has no way out to its catalogue

The last can be run by hand against a server that has one. A throwaway Emby comes with plugins older than its catalogue, so some are behind:

```bash
eval "$(EMBYFIN_METRICS_TEST_BACKEND=emby EMBYFIN_METRICS_TEST_ONLINE=1 .scripts/testenv.sh up)"
go test -tags acceptance -count=1 ./acceptance/ -run TestThePluginsAreOnThePage -v
EMBYFIN_METRICS_TEST_BACKEND=emby .scripts/testenv.sh down
```
