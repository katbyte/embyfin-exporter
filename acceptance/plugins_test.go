//go:build acceptance

package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
)

// online is set when the test server was started with a way out to the internet, which .scripts/testenv.sh does only when asked: the one thing a
// test needs it for is the server's catalogue of plugins.
var online = os.Getenv("EMBYFIN_EXPORTER_TEST_ONLINE") != ""

// onOffer is every version the test server's own catalogue has of each plugin, by the plugin's id without its dashes.
func onOffer(t *testing.T) map[string][]string {
	t.Helper()

	req, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodGet, address+"/Packages", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	var offers []struct {
		ID       string `json:"guid"`
		Versions []struct {
			Emby     string `json:"versionStr"`
			Jellyfin string `json:"version"`
		} `json:"versions"`
	}
	err = json.NewDecoder(resp.Body).Decode(&offers)
	_ = resp.Body.Close()

	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the server's catalogue: HTTP %d, %v", resp.StatusCode, err)
	}

	out := map[string][]string{}
	for _, offer := range offers {
		id := strings.ToLower(strings.ReplaceAll(offer.ID, "-", ""))
		for _, v := range offer.Versions {
			out[id] = append(out[id], v.Emby+v.Jellyfin)
		}
	}

	return out
}

// The plugins of a real server are on the page: as many as the server itself lists, each with the version it lists, and the Playback Reporting
// plugin the test server was given among them. The test server has no way out to its catalogue, so the page does not say how many are behind,
// which is not the same as none.
//
// Started with EMBYFIN_EXPORTER_TEST_ONLINE set, the server reaches its catalogue and the page does say: as many behind as it has lines for, each
// line a plugin the server lists at that version, and the newer version one the server's own catalogue has of that plugin.
func TestThePluginsAreOnThePage(t *testing.T) {
	live(t)

	all, err := client.Plugins(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// Jellyfin goes on listing a version a newer one has replaced, and a plugin taken out, neither of which it has any more (seen on 12.2)
	listed := slices.DeleteFunc(all, func(p embyfin.Plugin) bool { return p.Status == "Superseded" || p.Status == "Deleted" })

	text := scrape(t, poll.RoundPlugins)

	want := map[string]float64{reading("embyfin_plugins"): float64(len(listed))}
	versions, ids, reporting := map[string]string{}, map[string]string{}, false
	for _, p := range listed {
		want[reading("embyfin_plugin_info", "plugin", p.Name, "version", p.Version)] = 1
		versions[p.Name], ids[p.Name] = p.Version, strings.ToLower(strings.ReplaceAll(p.ID, "-", ""))
		reporting = reporting || p.Name == "Playback Reporting"
	}
	expect(t, text, want)

	if !reporting || len(every(text, "embyfin_plugin_info")) != len(listed) {
		t.Errorf("the server lists %d plugins, Playback Reporting among them: %t, and the page has %d lines of them", len(listed), reporting, len(every(text, "embyfin_plugin_info")))
	}

	behind := every(text, "embyfin_plugin_update_available")
	count, said := value(text, reading("embyfin_plugins_update_available"))

	if !online {
		if said || len(behind) != 0 {
			t.Errorf("the server cannot reach its catalogue, and the page says %v plugins are behind (said: %t) and names %v", count, said, behind)
		}

		return
	}

	if !said || int(count) != len(behind) {
		t.Fatalf("the server can reach its catalogue, and the page says %v plugins are behind (said: %t) with %d lines of them", count, said, len(behind))
	}

	offered := onOffer(t)
	for _, line := range behind {
		plugin, version, latest := label(line, "plugin"), label(line, "version"), label(line, "latest")

		found := false
		for _, v := range offered[ids[plugin]] {
			found = found || v == latest
		}
		if versions[plugin] != version || !found || latest == version {
			t.Errorf("%s: the server lists it at %q and its catalogue has %v of it", line, versions[plugin], offered[ids[plugin]])
		}
	}

	t.Logf("%d of the server's %d plugins are behind its catalogue: %v", len(behind), len(listed), behind)
}
