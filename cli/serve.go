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
users and devices every --library-interval. A server that stops answering is reported as
embyfin_up 0, and does not stop the other being reported.

--labels puts labels of your choosing on every reading, hostname=nas say, for when the page
is not scraped on the servers' own host and the store has no other way to tell whose it is.

Every setting is also an environment variable: EMBYFIN_METRICS_EMBY_SERVER and
EMBYFIN_METRICS_EMBY_TOKEN, EMBYFIN_METRICS_JELLYFIN_SERVER and EMBYFIN_METRICS_JELLYFIN_TOKEN,
EMBYFIN_METRICS_LISTEN, EMBYFIN_METRICS_LABELS, EMBYFIN_METRICS_SESSIONS_INTERVAL and
EMBYFIN_METRICS_LIBRARY_INTERVAL.`,
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
