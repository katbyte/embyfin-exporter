package cli

import (
	"os/signal"
	"sync"
	"syscall"

	"github.com/katbyte/go-kt/pmetrics"
	"github.com/spf13/cobra"
)

func serveCmd(connect connector) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the metrics page",
		Long: `Asks each configured server for its state on a schedule and serves the last answers at
/metrics in the Prometheus text format, with a health probe at /healthz.

A scrape is answered from memory and never waits on a server. Sessions, streams, scheduled
tasks and the server's own state are read every --sessions-interval; libraries, item counts,
users and devices every --library-interval; and what was played every --history-interval,
from a server that has the Playback Reporting plugin. A server that stops answering is
reported as embyfin_up 0, and does not stop the other being reported.

No account's or device's name and no title is on the page unless --detail asks: users for
whose each session is, the time each account has played and when it was last seen, titles
for what each session is playing and the time each show and film has been played, devices
for the device and the app each session plays on, all for everything there is to name.

--labels puts labels of your choosing on every reading, hostname=nas say, for when the page
is not scraped on the servers' own host and the store has no other way to tell whose it is.

Every setting is also an environment variable: EMBYFIN_METRICS_EMBY_SERVER and
EMBYFIN_METRICS_EMBY_TOKEN, EMBYFIN_METRICS_JELLYFIN_SERVER and EMBYFIN_METRICS_JELLYFIN_TOKEN,
EMBYFIN_METRICS_LISTEN, EMBYFIN_METRICS_LABELS, EMBYFIN_METRICS_DETAIL,
EMBYFIN_METRICS_SESSIONS_INTERVAL, EMBYFIN_METRICS_LIBRARY_INTERVAL and
EMBYFIN_METRICS_HISTORY_INTERVAL.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			f := GetFlags()
			pollers, err := f.pollers(connect)
			if err != nil {
				return err
			}

			p, err := f.page(pollers)
			if err != nil {
				return err
			}

			// one signal stops both the polling and the serving
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			var wg sync.WaitGroup
			for _, poller := range pollers {
				wg.Go(func() { poller.Run(ctx) })
			}

			err = pmetrics.Serve(ctx, f.Listen, p.Handler())
			// serving can end by itself, on a port already taken say, and the polling has to end with it
			stop()
			wg.Wait()

			return err
		},
	}
}
