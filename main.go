// Package main implements embyfin-exporter, which reports the state of an Emby and a Jellyfin server as a Prometheus metrics page.
package main

import (
	"os"

	"github.com/gookit/color"
	"github.com/katbyte/embyfin-exporter/cli"
	"github.com/katbyte/go-kt/clog"
)

func main() {
	// the log level comes from EMBYFIN_EXPORTER_LOG; read it once here, before anything logs
	clog.SetLevelFromEnv("EMBYFIN_EXPORTER_LOG")

	cmd, err := cli.Make()
	if err != nil {
		clog.Log.Error(color.Sprintf("<red>embyfin-exporter: building cmd</> %v", err))

		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		clog.Log.Error(color.Sprintf("<red>embyfin-exporter:</> %v", err))

		os.Exit(1)
	}

	os.Exit(0)
}
