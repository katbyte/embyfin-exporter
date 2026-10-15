# Configuration

Every setting can be a command-line flag, an environment variable or a line in a configuration file. A flag or a variable wins over the file.

| Variable | Flag | What it is |
|---|---|---|
| `EMBYFIN_EXPORTER_EMBY_SERVER` | `--emby-server` | The Emby server's URL, e.g. `http://nas:8096`. |
| `EMBYFIN_EXPORTER_EMBY_TOKEN` | `--emby-token` | Its API key (dashboard → Advanced → API Keys). |
| `EMBYFIN_EXPORTER_JELLYFIN_SERVER` | `--jellyfin-server` | The Jellyfin server's URL, e.g. `http://nas:8097`. |
| `EMBYFIN_EXPORTER_JELLYFIN_TOKEN` | `--jellyfin-token` | Its API key (dashboard → API Keys). |
| `EMBYFIN_EXPORTER_LISTEN` | `--listen` | The address the page is served on. Default `:9096`. |
| `EMBYFIN_EXPORTER_LABELS` | `--labels` | [Labels of your own](#labels-of-your-own) on every reading. None by default. |
| `EMBYFIN_EXPORTER_DETAIL` | `--detail` | [Who and what the page may name](#names). Nobody and nothing by default. |
| `EMBYFIN_EXPORTER_INTERVALS` | `--intervals` | [How often each round is read](#intervals). `sessions=15s,library=5m,history=30s` by default. |
| `EMBYFIN_EXPORTER_LOG` | | The [log level](#log-level). Default `WARN`. |
| `EMBYFIN_EXPORTER_CONFIG` | `--config` | Read settings from [this file](#configuration-file) and no other. |

## Servers and keys

Either server can be left out by setting neither its address nor its key. An address with no key, or a key with no address, stops the program with the name of the variable that is missing.

A key is never logged and never on the page.

## Labels of your own

The program is an exporter. It usually runs beside the servers it reports on, on the same host, and leaves saying which host that is to whatever scrapes it.

It asks the servers over their own HTTP API, so nothing is installed in them and it can run somewhere else instead. `--labels` then puts labels of your choosing on every reading, to say whose they are. They are written as `name=value,name=value`, e.g. `hostname=nas`.

A label named as one a reading already has, `server` say, stops the program with its name.

## Names

`--detail` is a list of `users`, `titles`, `devices` or `all`, joined with commas. A word it does not know stops the program. [Names, only when you ask](readings.md#names-only-when-you-ask) says what each word adds.

## Intervals

Each server is asked in three rounds, each on its own clock:

| Round | What is read | Default |
|---|---|---|
| `sessions` | Sessions, streams, scheduled tasks and the server's own state. | `15s` |
| `library` | Libraries, item counts, users, devices, sign-ins and plugins. | `5m` |
| `history` | What was played, from a server with the Playback Reporting plugin. | `30s` |

`--intervals` sets any of them, written `name=duration` with commas between them: `sessions=30s,library=10m`. A round left out keeps its default. A name that is not a round, or a length of time of nothing, stops the program with what is wrong.

A round is given until the next is due, and a server's questions in a round are asked at once.

## Log level

`EMBYFIN_EXPORTER_LOG` is `WARN` by default.

- `INFO` adds what is being reported on, and when a server comes back.
- `TRACE` adds every request to a server and its answer, with keys blanked.

An answer names users and what they are playing. So `TRACE` is for finding a fault and not for leaving on.

## Configuration file

You can place a `.embyfin-exporter` file in your home directory, `~/.embyfin-exporter`, or in your current directory, `./.embyfin-exporter`. Keys match the long flag names in the `env` format, with an underscore between words:

```env
EMBY_SERVER=http://nas:8096
EMBY_TOKEN=...
JELLYFIN_SERVER=http://nas:8097
JELLYFIN_TOKEN=...
```

`--config` (or `EMBYFIN_EXPORTER_CONFIG`) names the file to read, and it is then the only one read. A file that is named and is not there stops the program from starting.
