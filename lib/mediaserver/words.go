// Package mediaserver puts the page's questions to an Emby or a Jellyfin and turns what the server says into the page's own words.
//
// This file is the words: what each server calls a kind of item, a way of playing or a task's state, and what the page calls it. The two servers
// share nearly all of them, having started as one.
package mediaserver

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/embyfin-metrics/lib/poll"
)

// kind is one kind of item: the page's word for it, and the type name the servers know it by.
type kind struct {
	label  string
	server string
}

var (
	movie      = kind{"movie", "Movie"}
	series     = kind{"series", "Series"}
	season     = kind{"season", "Season"}
	episode    = kind{"episode", "Episode"}
	artist     = kind{"artist", "MusicArtist"}
	album      = kind{"album", "MusicAlbum"}
	track      = kind{"track", "Audio"}
	musicVideo = kind{"music_video", "MusicVideo"}
	collection = kind{"collection", "BoxSet"}
	playlist   = kind{"playlist", "Playlist"}
	book       = kind{"book", "Book"}
	// audiobook is Jellyfin's alone. Emby has no such kind, and asked for a kind it does not know it counts everything it holds (seen on 4.11).
	audiobook = kind{"audiobook", "AudioBook"}
	video     = kind{"video", "Video"}
	photo     = kind{"photo", "Photo"}
)

// mixed is what is counted in a library that holds anything, or whose type is not one known here.
var mixed = []kind{movie, series, season, episode, video}

// kindsIn is the kinds of item counted in a library, by the collection type its server gives it. Each kind is a question of its own to the server,
// so only the kinds a library of that type holds are asked after: a film library is not asked for its albums.
//
// A kind is only asked of a server seen to answer for it by library. Jellyfin gives every artist it knows whichever library is asked after (seen on
// 12.2), so a music library's artists are counted on Emby alone, and Emby keeps an audiobook as an album of tracks.
func kindsIn(backend embyfin.Backend, collectionType string) []kind {
	emby := backend == embyfin.Emby

	switch strings.ToLower(collectionType) {
	case "movies":
		return []kind{movie}
	case "tvshows":
		return []kind{series, season, episode}
	case "music":
		if emby {
			return []kind{artist, album, track}
		}

		return []kind{album, track}
	case "musicvideos":
		return []kind{musicVideo}
	case "boxsets":
		return []kind{collection}
	case "playlists":
		return []kind{playlist}
	case "books":
		if emby {
			return []kind{book}
		}

		return []kind{book, audiobook}
	case "audiobooks":
		if emby {
			return []kind{album, track}
		}

		return []kind{audiobook}
	case "homevideos", "photos":
		return []kind{video, photo}
	default:
		return mixed
	}
}

// played is the kinds of item a session can be playing that the page tells apart.
var played = []kind{movie, episode, track, musicVideo, video, audiobook}

// playing is the kind of thing a session plays, from the type the server gives what is playing. Anything else a server can play, live television or
// a trailer say, is other.
func playing(itemType string) poll.Kind {
	for _, k := range played {
		if strings.EqualFold(itemType, k.server) {
			return poll.Kind(k.label)
		}
	}

	return poll.OtherKind
}

// sessionState is what a session is doing: idle with nothing playing, however its pause flag was left.
func sessionState(playing, paused bool) poll.SessionState {
	switch {
	case !playing:
		return poll.Idle
	case paused:
		return poll.Paused
	default:
		return poll.Playing
	}
}

// method is how a session plays, from the device's word for it. A word not known here is unknown rather than a guess.
func method(playMethod string) poll.Method {
	switch strings.ToLower(playMethod) {
	case "directplay":
		return poll.DirectPlay
	case "directstream":
		return poll.DirectStream
	case "transcode":
		return poll.Transcode
	default:
		return poll.UnknownMethod
	}
}

// ticks is a length of time as the servers count one, in ten-millionths of a second.
func ticks(n int64) time.Duration {
	return time.Duration(max(n, 0)) * 100
}

// network is where a session's device is, from the address the server says it connects from: at home when the address is one that only works on a
// private network or on the server's own machine, and remote for any other. The address goes no further than this.
func network(address string) poll.Network {
	addr, err := netip.ParseAddr(address)
	if err != nil {
		// with a port after it
		withPort, err := netip.ParseAddrPort(address)
		if err != nil {
			return poll.UnknownNetwork
		}
		addr = withPort.Addr()
	}

	switch addr = addr.Unmap(); {
	case addr.IsUnspecified():
		return poll.UnknownNetwork
	case addr.IsLoopback(), addr.IsPrivate(), addr.IsLinkLocalUnicast():
		return poll.Home
	default:
		return poll.Remote
	}
}

// nowPlaying is what a session is playing, written for someone to read: an episode with its show, season and number, a film with its year, a song
// with who it is by, and anything else by its name alone.
func nowPlaying(item *embyfin.Item) string {
	switch {
	case strings.EqualFold(item.Type, episode.server) && item.SeriesName != "" && item.ParentIndexNumber != nil && item.IndexNumber != nil:
		return fmt.Sprintf("%s - S%02dE%02d - %s", item.SeriesName, *item.ParentIndexNumber, *item.IndexNumber, item.Name)
	case strings.EqualFold(item.Type, episode.server) && item.SeriesName != "":
		return item.SeriesName + " - " + item.Name
	// a film the server has looked nothing up for is named as its folder is, which may have the year in it already
	case strings.EqualFold(item.Type, movie.server) && item.ProductionYear > 0 && !strings.HasSuffix(item.Name, fmt.Sprintf("(%d)", item.ProductionYear)):
		return fmt.Sprintf("%s (%d)", item.Name, item.ProductionYear)
	case len(item.Artists) > 0 && item.Artists[0] != "":
		return item.Artists[0] + " - " + item.Name
	default:
		return item.Name
	}
}

// reencoded says whether the server is re-encoding one of a session's streams: one it is not passing on as it is, and that is there at all. A song
// has no picture and a silent film no sound, and the server names no codec for the stream that is missing.
func reencoded(direct bool, codec string) bool {
	return !direct && codec != ""
}

// hardware says whether a GPU is encoding a transcode's picture. Emby says so of the encoder; Jellyfin names the kind of acceleration the transcode
// uses, and "none" for software.
func hardware(encoder *bool, acceleration string) bool {
	if encoder != nil {
		return *encoder
	}

	return acceleration != "" && !strings.EqualFold(acceleration, "none")
}

// reasons is why the server is making a stream rather than sending the file as it is, each of its reasons once and in the page's way of writing:
// ContainerNotSupported is container_not_supported. The servers have some thirty of them and add more, so they are rewritten and not listed here.
func reasons(words []string) []string {
	var out []string
	for _, word := range words {
		if reason := snake(word); reason != "" && !slices.Contains(out, reason) {
			out = append(out, reason)
		}
	}
	slices.Sort(out)

	return out
}

// snake is a word the servers write as WordsRunTogether written as words_run_together, with whatever is not a letter or a digit taken as a gap
// between two words.
func snake(word string) string {
	letters := []rune(word)

	var b strings.Builder
	var gap bool
	for i, r := range letters {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}

		// a new word starts at a capital after a small letter or a digit, and at the last capital of a run of them: HDRNot is hdr_not
		capital := unicode.IsUpper(r) && i > 0 && (!unicode.IsUpper(letters[i-1]) || (i+1 < len(letters) && unicode.IsLower(letters[i+1])))
		if (gap || capital) && b.Len() > 0 {
			b.WriteByte('_')
		}
		gap = false

		b.WriteRune(unicode.ToLower(r))
	}

	return b.String()
}

// progress is how far a running task has got, from 0 to 1, from the percentage the server gives.
func progress(percent float64) float64 {
	return min(max(percent/100, 0), 1)
}

// taskState is what a scheduled task is doing, from the server's word for it. A task in no state the servers are known to give is idle.
func taskState(state string) poll.TaskState {
	switch strings.ToLower(state) {
	case "running":
		return poll.TaskRunning
	case "cancelling":
		return poll.TaskCancelling
	default:
		return poll.TaskIdle
	}
}

// ranOK says whether a task's last run finished as it should, and failed whether it went wrong. The servers' other words for how one ended are
// Cancelled and Aborted, a run someone stopped or the server stopping cut short, which is neither.
func ranOK(status string) bool {
	return strings.EqualFold(status, "Completed")
}

func failed(status string) bool {
	return strings.EqualFold(status, "Failed")
}

// when reads a time as either server writes one, and gives the zero time for none or one it cannot read. Both write the fraction of a second to
// seven places, and Emby leaves the zone off some, which are then UTC.
func when(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.9999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}

	return time.Time{}
}

// between is how long passed from start to end, and nothing when either is missing or they are the wrong way round.
func between(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}

	return end.Sub(start)
}
