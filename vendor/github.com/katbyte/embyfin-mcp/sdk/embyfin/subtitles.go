package embyfin

import (
	"context"

	"github.com/katbyte/embyfin-mcp/sdk/emby/4.10.0.40/emby"
	"github.com/katbyte/embyfin-mcp/sdk/jf/12.1.0/jf"
)

type RemoteSubtitle struct {
	ID                         string  `json:"Id"`
	Name                       string  `json:"Name,omitempty"`
	ProviderName               string  `json:"ProviderName,omitempty"`
	Format                     string  `json:"Format,omitempty"`
	ThreeLetterISOLanguageName string  `json:"ThreeLetterISOLanguageName,omitempty"`
	DownloadCount              int     `json:"DownloadCount,omitempty"`
	CommunityRating            float64 `json:"CommunityRating,omitempty"`
	Comment                    string  `json:"Comment,omitempty"`
}

// SearchSubtitles lists remote subtitle candidates for an item in the given
// language (three-letter code, e.g. eng).
func (c *Client) SearchSubtitles(ctx context.Context, itemID, language string) ([]RemoteSubtitle, error) {
	if c.isEmby() {
		res, err := c.emby.Subtitle.GetItemsByIdRemoteSearchSubtitlesByLanguage(ctx, emby.ItemID(itemID), language, emby.GetItemsByIdRemoteSearchSubtitlesByLanguageOptions{})
		if err != nil {
			return nil, err
		}
		subs := make([]RemoteSubtitle, 0, len(res.Answer))
		for i := range res.Answer {
			subs = append(subs, remoteSubtitleFromEmby(&res.Answer[i]))
		}

		return subs, nil
	}

	res, err := c.jf.Subtitle.SearchRemoteSubtitles(ctx, jf.ItemID(itemID), language)
	if err != nil {
		return nil, err
	}
	subs := make([]RemoteSubtitle, 0, len(res.Answer))
	for i := range res.Answer {
		subs = append(subs, remoteSubtitleFromJF(&res.Answer[i]))
	}

	return subs, nil
}

// DownloadSubtitle downloads a chosen remote subtitle to sit beside the item.
func (c *Client) DownloadSubtitle(ctx context.Context, itemID, subtitleID string) error {
	if c.isEmby() {
		_, err := c.emby.Subtitle.PostItemsByIdRemoteSearchSubtitlesBySubtitleId(ctx, emby.ItemID(itemID), subtitleID, "")
		return err
	}

	_, err := c.jf.Subtitle.DownloadRemoteSubtitles(ctx, jf.ItemID(itemID), subtitleID)

	return err
}
