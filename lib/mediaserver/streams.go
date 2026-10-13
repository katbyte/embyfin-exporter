package mediaserver

import (
	"context"
	"net/http"

	apiclient "github.com/katbyte/pandorest/client"
)

// fileRates is what is asked of a server for what the files being played come to, so a test can answer in its place.
type fileRates interface {
	// FileRates is how many bits a second the file each playing session plays comes to, by the session's id.
	FileRates(ctx context.Context) (map[string]int, error)
}

// FileRates reads the sessions a second time for the one thing embyfin-mcp's session leaves out: what the file being played comes to, which is what
// leaves the server when the file is sent as it is. It is the bitrates of the file's streams added together, or the file's own where a server gives
// no stream one: Jellyfin gives a file none of its own, and Emby gives it its picture's alone (seen on 12.2 and 4.10).
func (d *direct) FileRates(ctx context.Context) (map[string]int, error) {
	req, err := d.base.NewRequest(ctx, apiclient.RequestOptions{ExpectedStatusCodes: []int{http.StatusOK}, HTTPMethod: http.MethodGet, Path: "/Sessions"})
	if err != nil {
		return nil, err
	}

	resp, err := req.Execute(ctx)
	if err != nil {
		return nil, err
	}

	var sessions []struct {
		ID      string `json:"Id"`
		Playing *struct {
			Bitrate int `json:"Bitrate"`
			Streams []struct {
				Bitrate int `json:"BitRate"`
			} `json:"MediaStreams"`
		} `json:"NowPlayingItem"`
	}
	if err := resp.Unmarshal(&sessions); err != nil {
		return nil, err
	}

	rates := map[string]int{}
	for _, session := range sessions {
		if session.Playing == nil {
			continue
		}

		var streams int
		for _, stream := range session.Playing.Streams {
			streams += max(stream.Bitrate, 0)
		}
		if rate := max(streams, session.Playing.Bitrate); rate > 0 {
			rates[session.ID] = rate
		}
	}

	return rates, nil
}
