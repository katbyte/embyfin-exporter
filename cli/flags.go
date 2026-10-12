package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/katbyte/embyfin-metrics/lib/readings"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/pmetrics"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// The servers the page can report on, as the server label names them.
const (
	kindEmby     = "emby"
	kindJellyfin = "jellyfin"
)

// The defaults: sessions are read twice as often as a scrape every 30 seconds asks, so a reading is never older than one scrape, and libraries
// every few minutes since they only change when a scan runs or someone is added.
const (
	defaultListen           = ":9096"
	defaultSessionsInterval = 15 * time.Second
	defaultLibraryInterval  = 5 * time.Minute
)

type FlagData struct {
	EmbyServer       string        `mapstructure:"emby-server"`
	EmbyToken        string        `mapstructure:"emby-token"`
	JellyfinServer   string        `mapstructure:"jellyfin-server"`
	JellyfinToken    string        `mapstructure:"jellyfin-token"`
	Listen           string        `mapstructure:"listen"`
	Labels           string        `mapstructure:"labels"`
	SessionsInterval time.Duration `mapstructure:"sessions-interval"`
	LibraryInterval  time.Duration `mapstructure:"library-interval"`
}

// envNames binds each flag to its environment variable.
var envNames = map[string]string{ //nolint:gosec // G101: these are env var names, not credentials
	"emby-server":       "EMBYFIN_METRICS_EMBY_SERVER",
	"emby-token":        "EMBYFIN_METRICS_EMBY_TOKEN",
	"jellyfin-server":   "EMBYFIN_METRICS_JELLYFIN_SERVER",
	"jellyfin-token":    "EMBYFIN_METRICS_JELLYFIN_TOKEN",
	"listen":            "EMBYFIN_METRICS_LISTEN",
	"labels":            "EMBYFIN_METRICS_LABELS",
	"sessions-interval": "EMBYFIN_METRICS_SESSIONS_INTERVAL",
	"library-interval":  "EMBYFIN_METRICS_LIBRARY_INTERVAL",
}

// persistent is the flag set configureFlags bound, for the one setting that has to be read before viper is: where the settings file is.
var persistent *pflag.FlagSet

func configureFlags(root *cobra.Command) error {
	pflags := root.PersistentFlags()

	pflags.String("emby-server", "", "the Emby server's url, e.g. http://nas:8096")
	pflags.String("emby-token", "", "the Emby API key (consider exporting to EMBYFIN_METRICS_EMBY_TOKEN instead)")
	pflags.String("jellyfin-server", "", "the Jellyfin server's url, e.g. http://nas:8097")
	pflags.String("jellyfin-token", "", "the Jellyfin API key (consider exporting to EMBYFIN_METRICS_JELLYFIN_TOKEN instead)")
	pflags.String("listen", defaultListen, "serve the metrics page on this address")
	pflags.String("labels", "", "labels to put on every reading, as name=value,name=value: hostname=nas say, when the page is not scraped on the servers' own host")
	pflags.Duration("sessions-interval", defaultSessionsInterval, "how often sessions, streams, scheduled tasks and the server's own state are read")
	pflags.Duration("library-interval", defaultLibraryInterval, "how often libraries, item counts, users and devices are read")
	pflags.String("config", "", "read settings from this file and no other, in place of ./.embyfin-metrics or ~/.embyfin-metrics (or export EMBYFIN_METRICS_CONFIG)")

	persistent = pflags

	for name, env := range envNames {
		if err := viper.BindPFlag(name, pflags.Lookup(name)); err != nil {
			return fmt.Errorf("error binding '%s' flag: %w", name, err)
		}

		if err := viper.BindEnv(name, env); err != nil {
			return fmt.Errorf("error binding '%s' to env '%s' : %w", name, env, err)
		}
	}

	return nil
}

// configEnv names the settings file from the environment, as --config does on the command line.
const configEnv = "EMBYFIN_METRICS_CONFIG"

// configPath is the settings file a run was told to read, "" for none: the flag's, then the environment's. A leading ~/ is the home directory, for
// whatever starts the program without a shell to expand it.
func configPath() string {
	path := os.Getenv(configEnv)
	if persistent != nil {
		if fl := persistent.Lookup("config"); fl != nil && fl.Changed {
			path = fl.Value.String()
		}
	}

	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, rest)
		}
	}

	return path
}

// loadConfig reads the settings file, once the flags are parsed: the one --config or EMBYFIN_METRICS_CONFIG names and no other, or else the first
// .embyfin-metrics found in the working directory and then in $HOME. A file that was named and cannot be read is an error; one merely absent from
// the usual places is not.
func loadConfig() error {
	viper.SetConfigType("env")
	if path := configPath(); path != "" {
		viper.SetConfigFile(path)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("reading the settings file %s (--config / %s): %w", path, configEnv, err)
		}
	} else {
		viper.SetConfigName(".embyfin-metrics")
		// viper reads the first file it finds, so the working directory comes first
		viper.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			viper.AddConfigPath(home)
		}

		if err := viper.ReadInConfig(); err != nil {
			if _, ok := errors.AsType[viper.ConfigFileNotFoundError](err); !ok {
				clog.Log.Errorf("Error reading config file: %v", err)
			}
		}
	}

	// a settings file spells a setting the way the environment does, less the prefix (EMBY_SERVER for --emby-server), and viper only matches a key
	// spelled as the flag is. Carried across as defaults, they still lose to a flag or the environment.
	for name := range envNames {
		if alt := strings.ReplaceAll(name, "-", "_"); alt != name && viper.InConfig(alt) {
			viper.SetDefault(name, viper.Get(alt))
		}
	}

	return nil
}

// GetFlags returns the fully populated FlagData. It is read from viper rather than from the flags themselves, since viper is what lays the
// environment and the settings file under the command line.
func GetFlags() *FlagData {
	var f FlagData
	if err := viper.Unmarshal(&f); err != nil {
		clog.Log.Fatalf("failed to unmarshal configuration: %v", err)
	}

	return &f
}

// target is one server to report on.
type target struct {
	kind   string
	server string
	token  string
}

// targets is the servers the settings name, Emby first. Either may be left out; a server with no key, or a key with no server, is a mistake said
// now rather than a page that quietly lacks a server.
func (f *FlagData) targets() ([]target, error) {
	var out []target
	for _, t := range []target{{kindEmby, f.EmbyServer, f.EmbyToken}, {kindJellyfin, f.JellyfinServer, f.JellyfinToken}} {
		serverFlag, tokenFlag := t.kind+"-server", t.kind+"-token"

		switch {
		case t.server == "" && t.token == "":
			continue
		case t.token == "":
			return nil, fmt.Errorf("--%s is set but --%s (%s) is not", serverFlag, tokenFlag, envNames[tokenFlag])
		case t.server == "":
			return nil, fmt.Errorf("--%s is set but --%s (%s) is not", tokenFlag, serverFlag, envNames[serverFlag])
		}

		out = append(out, t)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no server to report on: set --emby-server and --emby-token (%s, %s), --jellyfin-server and --jellyfin-token (%s, %s), or both pairs", envNames["emby-server"], envNames["emby-token"], envNames["jellyfin-server"], envNames["jellyfin-token"])
	}

	return out, nil
}

// connector makes the client that asks one server its questions.
type connector func(kind, server, token string) (poll.Server, error)

// pollers is a poller for each server the settings name, none of them started.
func (f *FlagData) pollers(connect connector) ([]*poll.Poller, error) {
	targets, err := f.targets()
	if err != nil {
		return nil, err
	}

	pollers := make([]*poll.Poller, 0, len(targets))
	for _, t := range targets {
		server, err := connect(t.kind, t.server, t.token)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.kind, err)
		}

		p, err := poll.New(server, poll.Intervals{Sessions: f.SessionsInterval, Library: f.LibraryInterval})
		if err != nil {
			return nil, err
		}

		// the address is logged and the key never is
		clog.Log.Infof("%s: reporting on %s, sessions every %s and libraries every %s", t.kind, t.server, f.SessionsInterval, f.LibraryInterval)
		pollers = append(pollers, p)
	}

	return pollers, nil
}

// name is what the program is called, on the page's front and in its help.
const name = "embyfin-metrics"

// page is the metrics page of these pollers' readings, with the labels the settings ask for on every one. A label that cannot be used is said
// here, before any server is asked anything: one written wrongly, or one named as a reading's own label is, server say.
func (f *FlagData) page(pollers []*poll.Poller) (*pmetrics.Page, error) {
	labels, err := pmetrics.ParseLabels(f.Labels)
	if err != nil {
		return nil, fmt.Errorf("--labels (%s): %w", envNames["labels"], err)
	}

	sources := make([]readings.Source, 0, len(pollers))
	for _, p := range pollers {
		sources = append(sources, p)
	}

	p, err := pmetrics.New(pmetrics.Options{Name: name, Namespace: readings.Namespace, Labels: labels}, readings.New(sources...))
	if err != nil {
		return nil, fmt.Errorf("--labels (%s): %w", envNames["labels"], err)
	}

	return p, nil
}
