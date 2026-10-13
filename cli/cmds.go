// Package cli implements the embyfin-metrics command line interface: the cobra commands, flag and config handling, and the server that serves the
// metrics page.
package cli

import (
	"fmt"
	"sync"

	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/katbyte/go-kt/version"
	"github.com/spf13/cobra"
)

func Make() (*cobra.Command, error) {
	return makeWith(connect)
}

// makeWith is Make with the servers' clients made by connect, so a test can stand in for a server.
func makeWith(connect connector) (*cobra.Command, error) {
	root := &cobra.Command{
		Use:   "embyfin-metrics [command]",
		Short: "embyfin-metrics reports the state of an Emby and a Jellyfin server as a Prometheus metrics page",
		Long: `Reports the state of an Emby and a Jellyfin server as a Prometheus metrics page: libraries
and what is in them, users, sessions and streams, scheduled tasks, and what has been played.
One process covers both servers, and only ever reads from them.
Complete documentation is available at https://github.com/katbyte/embyfin-metrics`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Run \"embyfin-metrics help\" for more information about available embyfin-metrics commands.")
			return nil
		},
	}

	root.AddCommand(&cobra.Command{
		Use:           "version",
		Short:         "Print the version number of embyfin-metrics",
		Long:          `Print the version number of embyfin-metrics`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "embyfin-metrics "+version.Version)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "print",
		Short: "Ask each server once and print the metrics page",
		Long: `Asks each configured server every question once and prints the page a scrape would read,
without serving anything: a check that the addresses and keys work, and a look at what
would be reported.`,
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

			var wg sync.WaitGroup
			for _, p := range pollers {
				for _, round := range poll.Rounds {
					wg.Go(func() { p.Poll(cmd.Context(), round) })
				}
			}
			wg.Wait()

			if err := p.Write(cmd.OutOrStdout()); err != nil {
				return err
			}

			// the page is printed either way, with what a server that did not answer looks like on it; what went wrong is in the log
			for _, p := range pollers {
				s := p.State()
				for _, round := range poll.Rounds {
					if !s.Round(round).OK {
						return fmt.Errorf("%s did not answer every question", p.Kind())
					}
				}
			}

			return nil
		},
	})

	root.AddCommand(serveCmd(connect))

	if err := configureFlags(root); err != nil {
		return nil, fmt.Errorf("unable to configure flags: %w", err)
	}
	// the settings file is read once the flags are parsed, since a flag can name it; before any command's own checks, which read what it holds
	root.PersistentPreRunE = func(*cobra.Command, []string) error { return loadConfig() }

	return root, nil
}
