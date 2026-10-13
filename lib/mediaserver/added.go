package mediaserver

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
	apiclient "github.com/katbyte/pandorest/client"
)

// newcomers is what is asked of a server for how many items of a library are new, so a test can answer in its place.
type newcomers interface {
	// Added is how many items of those kinds one library has that were added since a moment, counted by the server without handing any of them
	// over.
	Added(ctx context.Context, library, kinds string, since time.Time) (int, error)
}

// Added asks Emby to count a library's items added since a moment, which it does in a few hundredths of a second and embyfin-mcp has no word for.
// Jellyfin is not asked: it takes no such question, and counts the whole library whatever date it is given (seen on 12.2).
func (d *direct) Added(ctx context.Context, library, kinds string, since time.Time) (int, error) {
	query := url.Values{"Recursive": {"true"}, "ParentId": {library}, "IncludeItemTypes": {kinds}, "MinDateCreated": {since.UTC().Format(time.RFC3339)}, "Limit": {"0"}}

	req, err := d.base.NewRequest(ctx, apiclient.RequestOptions{ExpectedStatusCodes: []int{http.StatusOK}, HTTPMethod: http.MethodGet, Path: "/Items?" + query.Encode()})
	if err != nil {
		return 0, err
	}

	resp, err := req.Execute(ctx)
	if err != nil {
		return 0, err
	}

	var answer struct {
		Total int `json:"TotalRecordCount"`
	}
	if err := resp.Unmarshal(&answer); err != nil {
		return 0, err
	}

	return answer.Total, nil
}

// holders is the kinds that only hold others: a show and its seasons hold episodes, an artist and an album tracks. They are counted in a library and
// are not what someone means by an item added to it.
var holders = []kind{series, season, artist, album}

// arrivals is the kinds of a library whose arrival is an item added: the films, episodes, tracks, books and the like among the kinds it is counted
// for, as the servers know them, with commas between.
func arrivals(kinds []kind) string {
	var out []string
	for _, k := range kinds {
		if !slices.Contains(holders, k) {
			out = append(out, k.server)
		}
	}

	return strings.Join(out, ",")
}

// added is how many of a library's items were added within each window of now, for a server that can count them. One count failing is the
// whole answer failing, as with a count of what the library holds.
func (s *Server) added(ctx context.Context, library string, kinds []kind, now time.Time) (map[string]int, error) {
	what := arrivals(kinds)
	if s.arrivals == nil || what == "" {
		return nil, nil //nolint:nilnil // no answer is the answer for a server that cannot count them
	}

	out := map[string]int{}
	for _, window := range poll.Windows {
		n, err := s.arrivals.Added(ctx, library, what, now.Add(-window.Span))
		if err != nil {
			return nil, err
		}
		out[window.Label] = n
	}

	return out, nil
}
