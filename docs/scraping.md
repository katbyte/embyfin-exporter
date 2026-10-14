# Scraping it

The page is at `/metrics` on port 9096, with a health probe at `/healthz`.

## Docker

[`docker-compose.yml`](../docker-compose.yml) runs the released image beside the servers. The two keys go in a `.env` file next to it, see [`.env.example`](../.env.example):

```bash
cp .env.example .env   # then put the two API keys in it
docker compose up -d
curl -s http://127.0.0.1:9096/metrics
```

The example publishes the port on `127.0.0.1` only. The page needs no password, and whatever scrapes it usually runs on the same host.

## Prometheus

```yaml
scrape_configs:
  - job_name: embyfin
    scrape_interval: 30s
    static_configs:
      - targets: ['127.0.0.1:9096']
```

## Grafana Alloy

```alloy
prometheus.scrape "embyfin" {
  targets         = [{"__address__" = "127.0.0.1:9096"}]
  job_name        = "embyfin"
  scrape_interval = "30s"
  forward_to      = [prometheus.remote_write.default.receiver]
}
```

## How fresh a scrape is

A scrape is answered from memory and never waits on a server. The servers are asked on their own schedule, and the page holds the last answers.

With the default 15 second sessions interval, a scrape every 30 seconds never reads a session count older than 15 seconds.

## A few things to ask of it

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
embyfin_plugins_update_available > 0                               # a server has plugins with a newer version on offer
```

Anything with a `week` label is kept a week at a time, so `sum` the `week` away as the query for seconds played does. [Add the weeks together](readings.md#add-the-weeks-together) says why.

More queries sit beside the readings they use: [who is streaming now](readings.md#who-is-streaming-now), [what was played](readings.md#what-was-played) and [sign-ins](readings.md#sign-ins).
