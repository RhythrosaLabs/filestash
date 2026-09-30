package plg_widget_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	. "github.com/mickael-kerjean/filestash/server/common"
)

// YouTube through the YouTube Data API v3. Connecting needs an OAuth client
// from a Google Cloud project (see README), the user then signs in with Google
// and we keep the refresh token.
type YouTube struct{}

var (
	googleAuthURL    = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL   = "https://oauth2.googleapis.com/token"
	youtubeAPI       = "https://www.googleapis.com/youtube/v3"
	youtubeUploadURL = "https://www.googleapis.com/upload/youtube/v3/videos"
	youtubeScopes    = "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.force-ssl"
	youtubeHTTP      = &http.Client{} // no global timeout: uploads can be long, requests carry a context
)

func (this YouTube) Title() string    { return "YouTube" }
func (this YouTube) Fields() []string { return []string{"client_id", "client_secret", "privacy"} }
func (this YouTube) MaxLength() int   { return 5000 }
func (this YouTube) CaptionHint() string {
	return "Format for YouTube: the first line is the video title (under 90 characters, no < or >), then an empty line, then the description."
}

// ---------------------------------------------------------------- oauth

func (this YouTube) AuthURL(creds map[string]string, redirect, state string) string {
	return googleAuthURL + "?" + url.Values{
		"client_id":     {creds["client_id"]},
		"redirect_uri":  {redirect},
		"response_type": {"code"},
		"scope":         {youtubeScopes},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}.Encode()
}

func (this YouTube) Exchange(ctx context.Context, creds map[string]string, code, redirect string) error {
	var out struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := doJSON(ctx, "POST", googleTokenURL, map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		strings.NewReader(url.Values{
			"code":          {code},
			"client_id":     {creds["client_id"]},
			"client_secret": {creds["client_secret"]},
			"redirect_uri":  {redirect},
			"grant_type":    {"authorization_code"},
		}.Encode()), &out); err != nil {
		return err
	}
	if out.RefreshToken == "" {
		return fmt.Errorf("google didn't return a refresh token, remove the app from https://myaccount.google.com/permissions and connect again")
	}
	creds["refresh_token"] = out.RefreshToken
	return nil
}

type cachedToken struct {
	token   string
	expires time.Time
}

var youtubeTokens sync.Map // refresh token -> cachedToken

func (this YouTube) accessToken(ctx context.Context, creds map[string]string) (string, error) {
	if creds["refresh_token"] == "" {
		return "", fmt.Errorf("this YouTube account isn't signed in, connect it again from 🔗")
	}
	if c, ok := youtubeTokens.Load(creds["refresh_token"]); ok && time.Now().Before(c.(cachedToken).expires) {
		return c.(cachedToken).token, nil
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := doJSON(ctx, "POST", googleTokenURL, map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		strings.NewReader(url.Values{
			"client_id":     {creds["client_id"]},
			"client_secret": {creds["client_secret"]},
			"refresh_token": {creds["refresh_token"]},
			"grant_type":    {"refresh_token"},
		}.Encode()), &out); err != nil {
		return "", fmt.Errorf("google sign in expired or was revoked, connect the account again from 🔗 (%w)", err)
	}
	youtubeTokens.Store(creds["refresh_token"], cachedToken{out.AccessToken, time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second)})
	return out.AccessToken, nil
}

func (this YouTube) get(ctx context.Context, creds map[string]string, path string, params url.Values, out any) error {
	token, err := this.accessToken(ctx, creds)
	if err != nil {
		return err
	}
	return doJSON(ctx, "GET", youtubeAPI+path+"?"+params.Encode(), map[string]string{"Authorization": "Bearer " + token}, nil, out)
}

type youtubeChannel struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title     string `json:"title"`
			CustomURL string `json:"customUrl"`
		} `json:"snippet"`
		ContentDetails struct {
			RelatedPlaylists struct {
				Uploads string `json:"uploads"`
			} `json:"relatedPlaylists"`
		} `json:"contentDetails"`
	} `json:"items"`
}

func (this YouTube) channel(ctx context.Context, creds map[string]string) (youtubeChannel, error) {
	var ch youtubeChannel
	if err := this.get(ctx, creds, "/channels", url.Values{"part": {"snippet,contentDetails"}, "mine": {"true"}}, &ch); err != nil {
		return ch, err
	}
	if len(ch.Items) == 0 {
		return ch, fmt.Errorf("this Google account has no YouTube channel")
	}
	return ch, nil
}

func (this YouTube) Verify(ctx context.Context, creds map[string]string) (string, error) {
	if p := creds["privacy"]; p != "" && p != "private" && p != "unlisted" && p != "public" {
		return "", fmt.Errorf("privacy must be private, unlisted or public")
	}
	ch, err := this.channel(ctx, creds)
	if err != nil {
		return "", err
	}
	if ch.Items[0].Snippet.CustomURL != "" {
		return ch.Items[0].Snippet.CustomURL, nil
	}
	return ch.Items[0].Snippet.Title, nil
}

// ---------------------------------------------------------------- publish

// splitTitle: first line is the title, the rest the description
func splitTitle(text string) (string, string) {
	title, desc, _ := strings.Cut(strings.TrimSpace(text), "\n")
	title = strings.NewReplacer("<", "", ">", "").Replace(strings.TrimSpace(title))
	if utf8.RuneCountInString(title) > 100 {
		title = string([]rune(title)[:99]) + "…"
	}
	if title == "" {
		title = "Untitled"
	}
	return title, strings.NewReplacer("<", "", ">", "").Replace(strings.TrimSpace(desc))
}

func (this YouTube) Post(ctx context.Context, creds map[string]string, text string, media []Media) (string, error) {
	if len(media) != 1 || !isVideo(media[0].Name) || media[0].Path == "" {
		return "", NewError("a YouTube post needs exactly one video", 400)
	}
	title, desc := splitTitle(text)
	if len(desc) > 5000 {
		return "", NewError("YouTube descriptions are limited to 5000 bytes", 400)
	}
	privacy := creds["privacy"]
	if privacy == "" {
		privacy = "private"
	}
	token, err := this.accessToken(ctx, creds)
	if err != nil {
		return "", err
	}
	f, err := os.Open(media[0].Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, _ := f.Stat()

	// resumable upload: create the session with the metadata, then send the file
	meta, _ := json.Marshal(map[string]any{
		"snippet": map[string]any{"title": title, "description": desc, "categoryId": "22"},
		"status":  map[string]any{"privacyStatus": privacy, "selfDeclaredMadeForKids": false},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", youtubeUploadURL+"?uploadType=resumable&part=snippet,status", strings.NewReader(string(meta)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Type", mimeOf(media[0].Name))
	req.Header.Set("X-Upload-Content-Length", fmt.Sprint(info.Size()))
	res, err := youtubeHTTP.Do(req)
	if err != nil {
		return "", err
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	location := res.Header.Get("Location")
	if res.StatusCode != http.StatusOK || location == "" {
		return "", fmt.Errorf("youtube refused the upload: %d %s", res.StatusCode, youtubeError(b))
	}
	req, _ = http.NewRequestWithContext(ctx, "PUT", location, f)
	req.ContentLength = info.Size()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mimeOf(media[0].Name))
	if res, err = youtubeHTTP.Do(req); err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, _ = io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("youtube upload failed: %d %s", res.StatusCode, youtubeError(b))
	}
	var video struct {
		ID string `json:"id"`
	}
	json.Unmarshal(b, &video)
	return "https://youtu.be/" + video.ID, nil
}

func youtubeError(b []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return truncate(string(b), 200)
}

// ---------------------------------------------------------------- inbox

// Inbox returns the latest comments on the channel's 5 most recent videos
func (this YouTube) Inbox(ctx context.Context, creds map[string]string, limit int) ([]InboxItem, error) {
	ch, err := this.channel(ctx, creds)
	if err != nil {
		return nil, err
	}
	var uploads struct {
		Items []struct {
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
			ContentDetails struct {
				VideoID string `json:"videoId"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	if err := this.get(ctx, creds, "/playlistItems", url.Values{"part": {"snippet,contentDetails"}, "playlistId": {ch.Items[0].ContentDetails.RelatedPlaylists.Uploads}, "maxResults": {"5"}}, &uploads); err != nil {
		return nil, err
	}
	items := []InboxItem{}
	for _, v := range uploads.Items {
		var threads struct {
			Items []struct {
				ID      string `json:"id"`
				Snippet struct {
					TopLevelComment struct {
						Snippet struct {
							AuthorDisplayName string `json:"authorDisplayName"`
							TextOriginal      string `json:"textOriginal"`
							PublishedAt       string `json:"publishedAt"`
						} `json:"snippet"`
					} `json:"topLevelComment"`
				} `json:"snippet"`
			} `json:"items"`
		}
		if this.get(ctx, creds, "/commentThreads", url.Values{"part": {"snippet"}, "videoId": {v.ContentDetails.VideoID}, "maxResults": {fmt.Sprint(limit)}, "order": {"time"}}, &threads) != nil {
			continue // comments disabled on this video
		}
		for _, t := range threads.Items {
			c := t.Snippet.TopLevelComment.Snippet
			when, _ := time.Parse(time.RFC3339, c.PublishedAt)
			items = append(items, InboxItem{
				Kind: "comment on: " + truncate(v.Snippet.Title, 40),
				From: c.AuthorDisplayName,
				Text: c.TextOriginal,
				URL:  "https://www.youtube.com/watch?v=" + v.ContentDetails.VideoID + "&lc=" + t.ID,
				Time: when,
			})
		}
	}
	return items, nil
}
