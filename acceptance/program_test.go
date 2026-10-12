//go:build acceptance

package acceptance_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/katbyte/embyfin-metrics/cli"
)

// The program itself, given the server as a deployment gives it, prints a page with the server up and its films counted, and the key nowhere on
// it. Run where no settings file or variable of the developer's own can reach it.
func TestTheProgramPrintsThePage(t *testing.T) {
	live(t)

	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, kind := range []string{"EMBY", "JELLYFIN"} {
		t.Setenv("EMBYFIN_METRICS_"+kind+"_SERVER", "")
		t.Setenv("EMBYFIN_METRICS_"+kind+"_TOKEN", "")
	}
	t.Setenv("EMBYFIN_METRICS_"+strings.ToUpper(backend)+"_SERVER", address)
	t.Setenv("EMBYFIN_METRICS_"+strings.ToUpper(backend)+"_TOKEN", token)

	cmd, err := cli.Make()
	if err != nil {
		t.Fatalf("making the command: %v", err)
	}

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"print"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("print: %v", err)
	}

	text := out.String()
	expect(t, text, map[string]float64{
		reading("embyfin_up"): 1,
		reading("embyfin_library_items", "library", filmLibrary, "type", "movie"): films,
	})

	if strings.Contains(text, token) {
		t.Error("the key is on the page")
	}

	other := "jellyfin"
	if backend == other {
		other = "emby"
	}
	if strings.Contains(text, `server="`+other+`"`) {
		t.Errorf("the page reports on a %s nobody named:\n%s", other, text)
	}
}
