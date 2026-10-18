package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/mediaserver"
	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-exporter/lib/readings"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/go-kt/prometheus/poller"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// The servers the page can report on, as the server label names them.
const (
	kindEmby     = "emby"
	kindJellyfin = "jellyfin"
)

const defaultListen = ":9096"

type FlagData struct {
	EmbyServer     string `mapstructure:"emby-server"`
	EmbyToken      string `mapstructure:"emby-token"`
	JellyfinServer string `mapstructure:"jellyfin-server"`
	JellyfinToken  string `mapstructure:"jellyfin-token"`
	Listen         string `mapstructure:"listen"`
	Labels         string `mapstructure:"labels"`
	Detail         string `mapstructure:"detail"`
	Intervals      string `mapstructure:"intervals"`
	Metrics        string `mapstructure:"metrics"`
	Skip           string `mapstructure:"skip"`
}

// envNames binds each flag to its environment variable.
var envNames = map[string]string{ //nolint:gosec // G101: these are env var names, not credentials
	"emby-server":     "EMBYFIN_EXPORTER_EMBY_SERVER",
	"emby-token":      "EMBYFIN_EXPORTER_EMBY_TOKEN",
	"jellyfin-server": "EMBYFIN_EXPORTER_JELLYFIN_SERVER",
	"jellyfin-token":  "EMBYFIN_EXPORTER_JELLYFIN_TOKEN",
	"listen":          "EMBYFIN_EXPORTER_LISTEN",
	"labels":          "EMBYFIN_EXPORTER_LABELS",
	"detail":          "EMBYFIN_EXPORTER_DETAIL",
	"intervals":       "EMBYFIN_EXPORTER_INTERVALS",
	"metrics":         "EMBYFIN_EXPORTER_METRICS",
	"skip":            "EMBYFIN_EXPORTER_SKIP",
}

// persistent is the flag set configureFlags bound, for the one setting that has to be read before viper is: where the settings file is.
var persistent *pflag.FlagSet

func configureFlags(root *cobra.Command) error {
	pflags := root.PersistentFlags()

	pflags.String("emby-server", "", "the Emby server's url, e.g. http://nas:8096")
	pflags.String("emby-token", "", "the Emby API key (consider exporting to EMBYFIN_EXPORTER_EMBY_TOKEN instead)")
	pflags.String("jellyfin-server", "", "the Jellyfin server's url, e.g. http://nas:8097")
	pflags.String("jellyfin-token", "", "the Jellyfin API key (consider exporting to EMBYFIN_EXPORTER_JELLYFIN_TOKEN instead)")
	pflags.String("listen", defaultListen, "serve the metrics page on this address")
	pflags.String("labels", "", "labels to put on every reading, as name=value,name=value: hostname=nas say, when the page is not scraped on the servers' own host")
	pflags.String("detail", "", "who and what the page may name, as a list of users, titles, devices or all: nobody and nothing unless asked, so no account's or device's name and no title is on the page by default")
	pflags.String("metrics", "", "the groups of readings to ask the servers for and put on the page, and no others, with commas between them: poll, sessions, reasons, streaming, tasks, plugins, libraries, accounts, sign-ins or played. Every group unless this says, and the server's own state always")
	pflags.String("skip", "", "groups of readings to leave out of those --metrics names, named as for it: plugins,played say. The servers are not asked for them")
	pflags.String("intervals", "", "how often each round is asked, as round=duration with commas between them: server, sessions and tasks every 15s, libraries, accounts, sign-ins and plugins every 5m, and played every 30s unless this says")
	pflags.String("config", "", "read settings from this file and no other, in place of ./.embyfin-exporter or ~/.embyfin-exporter (or export EMBYFIN_EXPORTER_CONFIG)")

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
const configEnv = "EMBYFIN_EXPORTER_CONFIG"

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

// loadConfig reads the settings file, once the flags are parsed: the one --config or EMBYFIN_EXPORTER_CONFIG names and no other, or else the first
// .embyfin-exporter found in the working directory and then in $HOME. A file that was named and cannot be read is an error; one merely absent from
// the usual places is not.
func loadConfig() error {
	viper.SetConfigType("env")
	if path := configPath(); path != "" {
		viper.SetConfigFile(path)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("reading the settings file %s (--config / %s): %w", path, configEnv, err)
		}
	} else {
		viper.SetConfigName(".embyfin-exporter")
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

// The words --detail takes, and the one that stands for all of them, now and in versions to come.
const (
	detailUsers   = "users"
	detailTitles  = "titles"
	detailDevices = "devices"
	detailAll     = "all"
)

// detail is who and what the settings let the page name: nobody and nothing unless they say so. A word not known is a mistake said now, since a
// page that quietly names less than was asked reads as a server with nothing played.
func (f *FlagData) detail() (mediaserver.Detail, error) {
	var d mediaserver.Detail
	for word := range strings.SplitSeq(f.Detail, ",") {
		switch strings.ToLower(strings.TrimSpace(word)) {
		case "":
		case detailUsers:
			d.Users = true
		case detailTitles:
			d.Titles = true
		case detailDevices:
			d.Devices = true
		case detailAll:
			d = mediaserver.Detail{Users: true, Titles: true, Devices: true}
		default:
			return mediaserver.Detail{}, fmt.Errorf("--detail (%s) takes %s, %s, %s or %s, with commas between them, and not %q", envNames["detail"], detailUsers, detailTitles, detailDevices, detailAll, strings.TrimSpace(word))
		}
	}

	return d, nil
}

// named is what the page will name, for the log: it should be plain from the first line that a page has people on it.
func named(d mediaserver.Detail) string {
	var what []string
	if d.Users {
		what = append(what, "accounts")
	}
	if d.Titles {
		what = append(what, "titles")
	}
	if d.Devices {
		what = append(what, "devices")
	}
	if len(what) == 0 {
		return "naming nobody"
	}

	return "naming " + strings.Join(what, ", ")
}

// connector makes the client that asks one server its questions, naming accounts and titles only as far as detail allows.
type connector func(kind, server, token string, detail mediaserver.Detail) (poll.Server, error)

// poller is the poller of every server the settings name, not yet started.
func (f *FlagData) poller(connect connector) (*poll.Poller, error) {
	targets, err := f.targets()
	if err != nil {
		return nil, err
	}

	detail, err := f.detail()
	if err != nil {
		return nil, err
	}

	every, err := poller.ParseIntervals(f.Intervals, poll.Every)
	if err != nil {
		return nil, fmt.Errorf("--intervals (%s): %w", envNames["intervals"], err)
	}

	chosen, err := poll.ParseGroups(f.Metrics)
	if err != nil {
		return nil, fmt.Errorf("--metrics (%s): %w", envNames["metrics"], err)
	}

	skipped, err := poll.ParseGroups(f.Skip)
	if err != nil {
		return nil, fmt.Errorf("--skip (%s): %w", envNames["skip"], err)
	}

	skip := poll.Leave(chosen, skipped)

	servers := make([]poll.Server, 0, len(targets))
	for _, t := range targets {
		server, err := connect(t.kind, t.server, t.token, detail)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.kind, err)
		}

		// the address is logged and the key never is
		clog.Log.Infof("%s is at %s, asked for %s, %s%s", t.kind, t.server, asked(every, skip.Runs()), named(detail), skipping(skip))
		servers = append(servers, server)
	}

	return poll.New(every, skip, servers...)
}

// asked is how often each round that runs is asked, for the log, rounds asked as often as one another named together: server, sessions and tasks
// every 15s, played every 30s.
func asked(every map[string]time.Duration, rounds []poll.Round) string {
	var lengths []time.Duration
	byLength := map[time.Duration][]string{}
	for _, round := range rounds {
		length := every[string(round)]
		if _, seen := byLength[length]; !seen {
			lengths = append(lengths, length)
		}
		byLength[length] = append(byLength[length], string(round))
	}

	said := make([]string, 0, len(lengths))
	for _, length := range lengths {
		words := byLength[length]
		if n := len(words); n > 1 {
			words = []string{strings.Join(words[:n-1], ", ") + " and " + words[n-1]}
		}
		said = append(said, words[0]+" every "+length.String())
	}

	return strings.Join(said, ", ")
}

// skipping is what the page leaves off, for the log: nothing when it leaves off nothing.
func skipping(skip poll.Skip) string {
	if len(skip) == 0 {
		return ""
	}

	return ", leaving off " + strings.ReplaceAll(skip.String(), ",", ", ")
}

// name is what the program is called, on the page's front and in its help.
const name = "embyfin-exporter"

// page is the metrics page of the poller's readings and its own, with the labels the settings ask for on every one. A label that cannot be used is
// said here, before any server is asked anything: one written wrongly, or one named as a reading's own label is, server say.
func (f *FlagData) page(p *poll.Poller) (*metrics.Page, error) {
	labels, err := metrics.ParseLabels(f.Labels)
	if err != nil {
		return nil, fmt.Errorf("--labels (%s): %w", envNames["labels"], err)
	}

	page, err := metrics.New(metrics.Options{Name: name, Labels: labels}, readings.Page(p)...)
	if err != nil {
		return nil, fmt.Errorf("--labels (%s): %w", envNames["labels"], err)
	}

	return page, nil
}
