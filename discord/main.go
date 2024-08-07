package discord

import (
	// "fmt"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/Arno500/go-plex-client"
	discordRP "github.com/fawni/rp"
	rpc "github.com/fawni/rp/rpc"
	i18npkg "github.com/nicksnyder/go-i18n/v2/i18n"
	"gitlab.com/Arno500/plex-richpresence/i18n"
	"gitlab.com/Arno500/plex-richpresence/settings"
	"gitlab.com/Arno500/plex-richpresence/types"
)

var currentPlayState types.PlayState
var discord *rpc.Client
var client = &http.Client{
	Timeout: 5 * time.Second,
}

const EMPTY_THUMB_STRING = "plex"

// InitDiscordClient prepares Discord's RPC API to allow Rich Presence
func InitDiscordClient() {
	if (discord == nil || !discord.Logged) {
		discordInstance, err := discordRP.NewClient("803556010307616788")
		if err != nil {
			log.Println(err)
			return
		}
		discord = discordInstance
	}
}

// LogoutDiscordClient logout from Discord
func LogoutDiscordClient() {
	discord.Logout()
}

func getThumbnailLink(thumbKey string, plexInstance *plex.Plex) string {
	if currentPlayState.Thumb.PlexThumbUrl == thumbKey {
		if currentPlayState.Thumb.ImgLink != EMPTY_THUMB_STRING {
			return currentPlayState.Thumb.ImgLink
		}
	}
	currentPlayState.Thumb.PlexThumbUrl = thumbKey
	plexThumbLink := fmt.Sprintf("%s/photo/:/transcode?width=450&height=253&minSize=1&upscale=1&X-Plex-Token=%s&url=%s", plexInstance.URL, plexInstance.Token, thumbKey)

	thumbResp, err := client.Get(plexThumbLink)
	if err != nil {
		log.Printf("Couldn't get thumbnail from Plex (%s)", err)
		currentPlayState.Thumb.ImgLink = EMPTY_THUMB_STRING
		return EMPTY_THUMB_STRING
	}
	defer thumbResp.Body.Close()
	b, err := io.ReadAll(thumbResp.Body)
	if err != nil {
		log.Printf("Couldn't read thumbnail from Plex (%s)", err)
		currentPlayState.Thumb.ImgLink = EMPTY_THUMB_STRING
		return EMPTY_THUMB_STRING
	}

	imgUrl, err := UploadImage(b, thumbKey)
	if err != nil {
		log.Println("Error uploading image to litterbox: ", err)
		currentPlayState.Thumb.ImgLink = EMPTY_THUMB_STRING
		return EMPTY_THUMB_STRING
	}
	currentPlayState.Thumb.ImgLink = imgUrl
	return imgUrl
}

// SetRichPresence allows to send Rich Presence informations to Plex from a session info
func SetRichPresence(session types.PlexStableSession) {
	InitDiscordClient()
	now := time.Now()
	currentPlayState.Alteration.Item = false
	currentPlayState.Alteration.Time = false
	activityInfos := rpc.Activity{
		LargeImage: "plex",
		LargeText:  "Plex",
	}
	if currentPlayState.PlayingItem == nil || currentPlayState.PlayingItem.Media.GUID.String() != session.Media.GUID.String() {
		currentPlayState.PlayingItem = &session
		currentPlayState.Alteration.Item = true
	}
	if currentPlayState.PlayState != session.Session.State {
		currentPlayState.PlayState = session.Session.State
		currentPlayState.Alteration.Time = true
	}
	if session.Session.State == "paused" {
		activityInfos.SmallImage = "pause"
		activityInfos.SmallText = i18n.Localizer.MustLocalize(&i18npkg.LocalizeConfig{
			DefaultMessage: &i18npkg.Message{
				ID:    "Paused",
				Other: "Paused",
			},
		})
	} else if (session.Session.State == "playing" || session.Session.State == "buffering") && session.Media.Type != "photo" {
		activityInfos.SmallImage = "play"
		activityInfos.SmallText = i18n.Localizer.MustLocalize(&i18npkg.LocalizeConfig{
			DefaultMessage: &i18npkg.Message{
				ID:    "NowPlaying",
				Other: "Playing",
			},
		})
		if session.Session.State == "playing" {
			timeResetThreshold, _ := time.ParseDuration("4s")
			progress, _ := time.ParseDuration(strconv.FormatInt(session.Session.ViewOffset / 1000, 10) + "s")
			if settings.StoredSettings.TimeMode == "elapsed" {
				calculatedStartTime := now.Add(-progress)
				if !(currentPlayState.LastCalculatedTime.Add(-timeResetThreshold).Before(calculatedStartTime) && currentPlayState.LastCalculatedTime.Add(timeResetThreshold).After(calculatedStartTime)) {
					log.Printf("A seek or a media change was detected, updating state...")
					currentPlayState.Alteration.Time = true
					currentPlayState.LastCalculatedTime = calculatedStartTime
				}
				activityInfos.Timestamps = &rpc.Timestamps{
					Start: &calculatedStartTime,
				}
			} else if settings.StoredSettings.TimeMode == "remaining" {
				duration, _ := time.ParseDuration(strconv.FormatInt(session.Media.Duration, 10) + "ms")
				remaining := duration - progress
				calculatedEndTime := now.Add(remaining)
				if !(currentPlayState.LastCalculatedTime.Add(-timeResetThreshold).Before(calculatedEndTime) && currentPlayState.LastCalculatedTime.Add(timeResetThreshold).After(calculatedEndTime)) {
					log.Printf("A seek or a media change was detected, updating state...")
					currentPlayState.Alteration.Time = true
					currentPlayState.LastCalculatedTime = calculatedEndTime
				}
				activityInfos.Timestamps = &rpc.Timestamps{
					Start: &calculatedEndTime,
					End:   &calculatedEndTime,
				}
			}
		} else {
			currentPlayState.Alteration.Time = true
		}
	} else if session.Media.Type == "photo" {
		activityInfos.SmallImage = "camera"
	} else {
		log.Printf("Nothing is playing, closing connection to Discord.")
		LogoutDiscordClient()
		return
	}

	if currentPlayState.Alteration.Item || currentPlayState.Alteration.Time {
		if session.Media.Type == "episode" {
			// Season - Ep and title
			activityInfos.State = fmt.Sprintf("%02dx%02d - %s", session.Media.ParentIndex, session.Media.Index, session.Media.Title)
			// Show
			activityInfos.Details = session.Media.GrandparentTitle
			activityInfos.LargeImage = getThumbnailLink(session.Media.GrandparentThumbnail, session.PlexInstance)
		} else if session.Media.Type == "movie" {
			var formattedMovieName string
			if session.Media.Year > 0 {
				formattedMovieName = fmt.Sprintf("%s (%s)", session.Media.Title, strconv.Itoa(session.Media.Year))
			} else {
				formattedMovieName = session.Media.Title
			}
			// Movie Director
			if len(session.Media.Director) > 0 {
				activityInfos.State = session.Media.Director[0].Tag
				activityInfos.Details = formattedMovieName
			} else {
				activityInfos.State = "(⌐■_■)"
				activityInfos.Details = formattedMovieName
			}
			activityInfos.LargeImage = getThumbnailLink(session.Media.Thumbnail, session.PlexInstance)
		} else if session.Media.Type == "track" {
			if session.Media.OriginalTitle != "" {
				activityInfos.State = session.Media.OriginalTitle
			} else {
				activityInfos.State = session.Media.GrandparentTitle
			}
			activityInfos.LargeImage = getThumbnailLink(session.Media.ParentThumbnail, session.PlexInstance)
			activityInfos.Buttons = append(activityInfos.Buttons, &rpc.Button{
				Label: i18n.Localizer.MustLocalize(&i18npkg.LocalizeConfig{
				DefaultMessage: &i18npkg.Message{
					ID:    "TrackDetails",
					Other: "Track details on plex.tv",
				},
			}),
				Url:  fmt.Sprintf("https://listen.plex.tv/track/%s?parentGuid=%s&grandparentGuid=%s", path.Base(session.Media.GUID.EscapedPath()), path.Base(session.Media.ParentGUID.EscapedPath()), path.Base(session.Media.GrandparentGUID.EscapedPath())),
			})
			activityInfos.Details = fmt.Sprintf("%s (%s)", session.Media.Title, session.Media.ParentTitle)
		} else if session.Media.Type == "photo" {
			text := i18n.Localizer.MustLocalize(&i18npkg.LocalizeConfig{
				DefaultMessage: &i18npkg.Message{
					ID:    "WatchingPhotos",
					Other: "Watching photos",
				},
			})
			activityInfos.State = text
			activityInfos.SmallText = text
			activityInfos.Details = session.Media.Title
		} else if session.Media.Type == "clip" {
			// Trailer data (preroll)
			activityInfos.State = session.Media.Title
			activityInfos.SmallText = "Preroll"
		}
		err := discord.SetActivity(&activityInfos)
		if err != nil {
			log.Printf("An error occured when setting the activity in Discord: %v", err)
			discord = nil
		} else {
			log.Printf("Discord activity set")
		}
	}
}
