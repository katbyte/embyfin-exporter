package cli

import (
	"os/signal"
	"syscall"

	"github.com/katbyte/go-kt/prometheus/exporter"
	"github.com/spf13/cobra"
)

func serveCmd(connect connector) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the metrics page",
		Long: `Asks each configured server for its state on a schedule and serves the last answers at
/metrics in the Prometheus text format, with a health probe at /healthz.

A scrape is answered from memory and never waits on a server. Each group of readings is asked
for in a round of its own: the server's own state, sessions and tasks every 15s, libraries,
accounts, sign-ins and plugins every 5m, and what was played every 30s, from a server that
has the Playback Reporting plugin. --intervals sets any of them, as plugins=1h,played=1m. A
server that stops answering is reported as embyfin_up 0, and does not stop the other being
reported.

No account's or device's name and no title is on the page unless --detail asks: users for
whose each session is, the time each account has played, its sign-ins and when it was last
seen, titles for what each session is playing and the time each show and film has been
played, devices for the device and the app each session plays on, all for everything
there is to name.

Every group is on the page unless --metrics names the only ones to ask for, from poll,
sessions, reasons, streaming, tasks, plugins, libraries, accounts, sign-ins and played, and
--skip leaves some of those off. The servers are not asked for a group left off; poll is
how the asking goes, the embyfin_poll_* readings, and has no round of its own.

--labels puts labels of your choosing on every reading, hostname=nas say, for when the page
is not scraped on the servers' own host and the store has no other way to tell whose it is.

Every setting is also an environment variable: EMBYFIN_EXPORTER_EMBY_SERVER and
EMBYFIN_EXPORTER_EMBY_TOKEN, EMBYFIN_EXPORTER_JELLYFIN_SERVER and EMBYFIN_EXPORTER_JELLYFIN_TOKEN,
EMBYFIN_EXPORTER_LISTEN, EMBYFIN_EXPORTER_LABELS, EMBYFIN_EXPORTER_DETAIL,
EMBYFIN_EXPORTER_METRICS, EMBYFIN_EXPORTER_SKIP and EMBYFIN_EXPORTER_INTERVALS.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			f := GetFlags()
			p, err := f.poller(connect)
			if err != nil {
				return err
			}

			page, err := f.page(p)
			if err != nil {
				return err
			}

			// one signal stops both the polling and the serving, and serving that ends by itself, on a port already taken say, stops the polling
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return exporter.Serve(ctx, f.Listen, p, page)
		},
	}
}
