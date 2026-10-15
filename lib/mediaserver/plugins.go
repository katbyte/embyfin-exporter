package mediaserver

import (
	"cmp"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/outside"
	apiclient "github.com/katbyte/pandorest/client"
)

// catalogue is what is asked of a server for what its plugin catalogue has on offer, so a test can answer in its place.
type catalogue interface {
	// Offered is the newest version of each plugin that the server could run, by the plugin's id as pluginID writes it. It is empty when the
	// server could not reach its catalogue.
	Offered(ctx context.Context) (map[string]string, error)
}

// How long what a catalogue had on offer is kept before it is asked for again, and how long a server is given to say. A catalogue changes a few
// times a week and Emby's is over a megabyte; a server that cannot reach its own may take as long as it likes, and the libraries are not to wait.
const (
	catalogueKept = time.Hour
	catalogueWait = 20 * time.Second
)

// offer is one plugin in a server's catalogue with every version of it on offer. Emby says how settled a version is and the oldest server that
// can run it; Jellyfin says the server it was built for.
type offer struct {
	ID       string `json:"guid"`
	Versions []struct {
		Emby     string `json:"versionStr"`
		Class    string `json:"classification"`
		Needs    string `json:"requiredVersionStr"`
		Jellyfin string `json:"version"`
		BuiltFor string `json:"targetAbi"`
	} `json:"versions"`
}

// classes is how settled a version of an Emby plugin is, the most settled first. A server takes versions as far down as it is itself set to.
var classes = map[string]int{"Release": 0, "Beta": 1, "Dev": 2}

// get asks the server for one thing it only has to read, and puts its answer in into.
func (d *direct) get(ctx context.Context, path string, into any) error {
	req, err := d.base.NewRequest(ctx, apiclient.RequestOptions{ExpectedStatusCodes: []int{http.StatusOK}, HTTPMethod: http.MethodGet, Path: path})
	if err != nil {
		return err
	}

	resp, err := req.Execute(ctx)
	if err != nil {
		return err
	}

	return resp.Unmarshal(into)
}

// Offered reads the server's own catalogue of plugins, and its version to tell which of them it could run. A server with no way to its catalogue
// says so differently: Emby fails the question and Jellyfin answers with an empty list (seen on 4.10 and 12.2).
func (d *direct) Offered(ctx context.Context) (map[string]string, error) {
	var server struct {
		Version string `json:"Version"`
		Level   string `json:"SystemUpdateLevel"`
	}
	if err := d.get(ctx, "/System/Info", &server); err != nil {
		return nil, err
	}

	var offers []offer
	if err := d.get(ctx, "/Packages", &offers); err != nil {
		return nil, err
	}

	return newest(offers, server.Version, server.Level), nil
}

// newest is the newest version of each plugin in a catalogue that a server of that version could run, by the plugin's id. It is the choice each
// server makes for itself: Emby leaves out a version less settled than level, the server's own, or needing a newer server, and Jellyfin one built
// for a newer server.
func newest(offers []offer, server, level string) map[string]string {
	out := map[string]string{}
	for _, o := range offers {
		best := ""
		for _, v := range o.Versions {
			version, needs := cmp.Or(v.Emby, v.Jellyfin), cmp.Or(v.Needs, v.BuiltFor)

			class, known := classes[v.Class]
			if (v.Class != "" && (!known || class > classes[level])) || newer(needs, server) {
				continue
			}

			if _, readable := numbers(version); readable && (best == "" || newer(version, best)) {
				best = version
			}
		}

		if best != "" {
			out[pluginID(o.ID)] = best
		}
	}

	return out
}

// pluginID is a plugin's id written one way: Emby's catalogue and Jellyfin's write it with dashes, and Jellyfin's list of what is installed
// without.
func pluginID(id string) string {
	return strings.ToLower(strings.ReplaceAll(id, "-", ""))
}

// numbers is a version as its numbers, 1.0.28 as 1, 0 and 28, when it is nothing but numbers with dots between.
func numbers(version string) ([]int, bool) {
	var out []int
	for part := range strings.SplitSeq(version, ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}

	return out, true
}

// newer is whether one version is newer than another, where both are numbers with dots between. 1.0.28 and 1.0.28.0 are the same version: Emby
// writes what is installed with four numbers and what is on offer with as few as three.
func newer(version, than string) bool {
	a, ok := numbers(version)
	b, also := numbers(than)
	if !ok || !also {
		return false
	}

	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}

	return false
}

// removed is whether Jellyfin still lists a plugin that is no longer there: one taken out, or an older version a newer one has replaced.
func removed(p *embyfin.Plugin) bool {
	return p.Status == "Deleted" || p.Status == "Superseded"
}

// Plugins is what the server has installed, each with the newer version on offer when its catalogue has one. A plugin fetched and waiting for a
// restart is still behind on Emby, which lists the version that is running, and not on Jellyfin, which lists the new one beside the old.
func (s *Server) Plugins(ctx context.Context) (poll.Plugins, error) {
	installed, err := s.client.Plugins(ctx)
	if err != nil {
		return poll.Plugins{}, err
	}

	// the newest version of each that is installed, since Jellyfin can list two
	have := map[string]string{}
	for i := range installed {
		if p := &installed[i]; !removed(p) && (have[pluginID(p.ID)] == "" || newer(p.Version, have[pluginID(p.ID)])) {
			have[pluginID(p.ID)] = p.Version
		}
	}

	offered := s.onOffer(ctx)

	out := poll.Plugins{Installed: make([]poll.Plugin, 0, len(installed)), Checked: len(offered) != 0}
	for i := range installed {
		p := &installed[i]
		if removed(p) {
			continue
		}

		plugin := poll.Plugin{Name: p.Name, Version: p.Version}
		if latest := offered[pluginID(p.ID)]; p.Version == have[pluginID(p.ID)] && newer(latest, p.Version) {
			plugin.Latest = latest
		}
		out.Installed = append(out.Installed, plugin)
	}

	return out, nil
}

// onOffer is the newest version of each plugin the server could run, as its catalogue last had it, asked for at most once in catalogueKept. It
// is empty when the catalogue did not answer: the plugins are still the plugins, and which of them are behind is then not known rather than none.
func (s *Server) onOffer(ctx context.Context) map[string]string {
	if s.catalogue == nil {
		return nil
	}
	if len(s.offered) != 0 && time.Since(s.offeredAt) < catalogueKept {
		return s.offered
	}

	ctx, cancel := context.WithTimeout(ctx, catalogueWait)
	defer cancel()

	offered, err := s.catalogue.Offered(ctx)
	s.noteCatalogue(len(offered) != 0, err)

	s.offered, s.offeredAt = offered, time.Now()

	return offered
}

// noteCatalogue says in the log, once and again only when it changes, whether the server's catalogue of plugins answered: without it the page
// lists the plugins and does not say which are behind.
func (s *Server) noteCatalogue(answered bool, err error) {
	state := int32(1)
	if answered {
		state = 2
	}
	if s.catalogued.Swap(state) == state {
		return
	}

	switch {
	case answered:
		clog.Log.Infof("%s: reading which plugins have a newer version from the server's catalogue", s.Kind())
	case err != nil:
		clog.Log.Infof("%s: the server's catalogue of plugins did not answer, so the page does not say which are behind: %s", s.Kind(), strings.ReplaceAll(outside.BlankAddresses(err.Error()), "\n", "; "))
	default:
		clog.Log.Infof("%s: the server's catalogue of plugins is empty, so the page does not say which are behind", s.Kind())
	}
}
