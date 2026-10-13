package cli

import (
	"github.com/katbyte/embyfin-metrics/lib/mediaserver"
	"github.com/katbyte/embyfin-metrics/lib/poll"
)

// connect makes the client that asks one server its questions: embyfin-mcp's, which speaks to either kind.
func connect(kind, server, token string, detail mediaserver.Detail) (poll.Server, error) {
	s, err := mediaserver.Connect(kind, server, token, detail)
	if err != nil {
		return nil, err
	}

	return s, nil
}
