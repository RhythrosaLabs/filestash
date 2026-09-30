package plg_widget_ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/mickael-kerjean/filestash/server/common"
)

type InboxItem struct {
	Kind string    `json:"kind"` // reply, mention, like, follow, repost, comment, message, ...
	From string    `json:"from"`
	Text string    `json:"text"`
	URL  string    `json:"url,omitempty"`
	Time time.Time `json:"time"`
}

type Media struct {
	Name      string // staged file name
	Data      []byte
	PublicURL string // for providers that fetch media themselves (instagram)
}

type Provider interface {
	Title() string
	Fields() []string // credentials asked when connecting an account
	MaxLength() int
	Verify(ctx context.Context, creds map[string]string) (name string, err error)
	Post(ctx context.Context, creds map[string]string, text string, media []Media) (url string, err error)
	Inbox(ctx context.Context, creds map[string]string, limit int) ([]InboxItem, error)
}

var providers = map[string]Provider{
	"bluesky":   Bluesky{},
	"mastodon":  Mastodon{},
	"instagram": Instagram{},
}

var socialHTTP = &http.Client{Timeout: 60 * time.Second}

// doJSON sends a request and decodes a json response, surfacing the API error message
func doJSON(ctx context.Context, method, u string, headers map[string]string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := socialHTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 5<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
			Error   any    `json:"error"`
		}
		json.Unmarshal(b, &e)
		msg := e.Message
		if m, ok := e.Error.(map[string]any); ok && msg == "" {
			msg, _ = m["message"].(string)
		} else if s, ok := e.Error.(string); ok && msg == "" {
			msg = s
		}
		if msg == "" {
			msg = truncate(string(b), 200)
		}
		return fmt.Errorf("%d %s", res.StatusCode, msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

var htmlTags = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p><p>", "\n\n").Replace(s)
	s = htmlTags.ReplaceAllString(s, "")
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'").Replace(s)
}

func checkLength(p Provider, text string) error {
	if n := utf8.RuneCountInString(text); n > p.MaxLength() {
		return NewError(fmt.Sprintf("%s posts are limited to %d characters, this one has %d", p.Title(), p.MaxLength(), n), 400)
	}
	return nil
}

// ================================================================ Bluesky

type Bluesky struct{}

func (this Bluesky) Title() string    { return "Bluesky" }
func (this Bluesky) Fields() []string { return []string{"handle", "app_password", "service"} }
func (this Bluesky) MaxLength() int   { return 300 }

type bskySession struct {
	AccessJwt string `json:"accessJwt"`
	Did       string `json:"did"`
	Handle    string `json:"handle"`
	service   string
}

func (this Bluesky) login(ctx context.Context, creds map[string]string) (bskySession, error) {
	var s bskySession
	s.service = strings.TrimSuffix(creds["service"], "/")
	if s.service == "" {
		s.service = "https://bsky.social"
	}
	err := doJSON(ctx, "POST", s.service+"/xrpc/com.atproto.server.createSession",
		map[string]string{"Content-Type": "application/json"},
		jsonBody(map[string]string{"identifier": strings.TrimPrefix(creds["handle"], "@"), "password": creds["app_password"]}), &s)
	return s, err
}

func (this bskySession) auth(extra map[string]string) map[string]string {
	h := map[string]string{"Authorization": "Bearer " + this.AccessJwt}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func (this Bluesky) Verify(ctx context.Context, creds map[string]string) (string, error) {
	s, err := this.login(ctx, creds)
	if err != nil {
		return "", err
	}
	return "@" + s.Handle, nil
}

var linkRegex = regexp.MustCompile(`https?://[^\s<>"]+[^\s<>".,;:!?)]`)

func (this Bluesky) Post(ctx context.Context, creds map[string]string, text string, media []Media) (string, error) {
	if err := checkLength(this, text); err != nil {
		return "", err
	}
	if len(media) > 4 {
		return "", NewError("Bluesky allows up to 4 images per post", 400)
	}
	s, err := this.login(ctx, creds)
	if err != nil {
		return "", err
	}
	record := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      text,
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	}
	// links are only clickable with facets, indexed in bytes
	facets := []any{}
	for _, loc := range linkRegex.FindAllStringIndex(text, -1) {
		facets = append(facets, map[string]any{
			"index":    map[string]int{"byteStart": loc[0], "byteEnd": loc[1]},
			"features": []any{map[string]string{"$type": "app.bsky.richtext.facet#link", "uri": text[loc[0]:loc[1]]}},
		})
	}
	if len(facets) > 0 {
		record["facets"] = facets
	}
	images := []any{}
	for _, m := range media {
		if !isImage(m.Name) {
			return "", NewError("only images can be posted to Bluesky from here", 400)
		}
		data, mime := m.Data, mimeOf(m.Name)
		if len(data) > 950_000 {
			if data, err = toJPEG(data, 950_000); err != nil {
				return "", err
			}
			mime = "image/jpeg"
		}
		var out struct {
			Blob json.RawMessage `json:"blob"`
		}
		if err := doJSON(ctx, "POST", s.service+"/xrpc/com.atproto.repo.uploadBlob",
			s.auth(map[string]string{"Content-Type": mime}), bytes.NewReader(data), &out); err != nil {
			return "", fmt.Errorf("image upload failed: %w", err)
		}
		images = append(images, map[string]any{"alt": "", "image": out.Blob})
	}
	if len(images) > 0 {
		record["embed"] = map[string]any{"$type": "app.bsky.embed.images", "images": images}
	}
	var out struct {
		URI string `json:"uri"`
	}
	if err := doJSON(ctx, "POST", s.service+"/xrpc/com.atproto.repo.createRecord",
		s.auth(map[string]string{"Content-Type": "application/json"}),
		jsonBody(map[string]any{"repo": s.Did, "collection": "app.bsky.feed.post", "record": record}), &out); err != nil {
		return "", err
	}
	parts := strings.Split(out.URI, "/")
	return "https://bsky.app/profile/" + s.Handle + "/post/" + parts[len(parts)-1], nil
}

func (this Bluesky) Inbox(ctx context.Context, creds map[string]string, limit int) ([]InboxItem, error) {
	s, err := this.login(ctx, creds)
	if err != nil {
		return nil, err
	}
	var notifs struct {
		Notifications []struct {
			Reason    string `json:"reason"`
			IndexedAt string `json:"indexedAt"`
			IsRead    bool   `json:"isRead"`
			Author    struct {
				Handle string `json:"handle"`
			} `json:"author"`
			Record struct {
				Text string `json:"text"`
			} `json:"record"`
		} `json:"notifications"`
	}
	if err := doJSON(ctx, "GET", fmt.Sprintf("%s/xrpc/app.bsky.notification.listNotifications?limit=%d", s.service, limit),
		s.auth(nil), nil, &notifs); err != nil {
		return nil, err
	}
	items := []InboxItem{}
	for _, n := range notifs.Notifications {
		t, _ := time.Parse(time.RFC3339, n.IndexedAt)
		items = append(items, InboxItem{Kind: n.Reason, From: "@" + n.Author.Handle, Text: n.Record.Text, Time: t})
	}
	// direct messages need an app password with DM access, skip silently otherwise
	var convos struct {
		Convos []struct {
			UnreadCount int `json:"unreadCount"`
			Members     []struct {
				Handle string `json:"handle"`
			} `json:"members"`
			LastMessage struct {
				Text   string `json:"text"`
				SentAt string `json:"sentAt"`
				Sender struct {
					Did string `json:"did"`
				} `json:"sender"`
			} `json:"lastMessage"`
		} `json:"convos"`
	}
	if doJSON(ctx, "GET", s.service+"/xrpc/chat.bsky.convo.listConvos?limit=10",
		s.auth(map[string]string{"atproto-proxy": "did:web:api.bsky.chat#bsky_chat"}), nil, &convos) == nil {
		for _, c := range convos.Convos {
			if c.UnreadCount == 0 {
				continue
			}
			from := []string{}
			for _, m := range c.Members {
				if m.Handle != s.Handle {
					from = append(from, "@"+m.Handle)
				}
			}
			t, _ := time.Parse(time.RFC3339, c.LastMessage.SentAt)
			items = append(items, InboxItem{Kind: fmt.Sprintf("message (%d unread)", c.UnreadCount), From: strings.Join(from, ", "), Text: c.LastMessage.Text, Time: t})
		}
	}
	return items, nil
}

// ================================================================ Mastodon

type Mastodon struct{}

func (this Mastodon) Title() string    { return "Mastodon" }
func (this Mastodon) Fields() []string { return []string{"instance", "access_token"} }
func (this Mastodon) MaxLength() int   { return 500 }

func (this Mastodon) base(creds map[string]string) string {
	u := strings.TrimSuffix(strings.TrimSpace(creds["instance"]), "/")
	if u != "" && !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	return u
}

func (this Mastodon) auth(creds map[string]string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + creds["access_token"]}
}

func (this Mastodon) Verify(ctx context.Context, creds map[string]string) (string, error) {
	var out struct {
		Acct string `json:"acct"`
	}
	if err := doJSON(ctx, "GET", this.base(creds)+"/api/v1/accounts/verify_credentials", this.auth(creds), nil, &out); err != nil {
		return "", err
	}
	host, _ := url.Parse(this.base(creds))
	return "@" + out.Acct + "@" + host.Host, nil
}

func (this Mastodon) Post(ctx context.Context, creds map[string]string, text string, media []Media) (string, error) {
	if err := checkLength(this, text); err != nil {
		return "", err
	}
	if len(media) > 4 {
		return "", NewError("Mastodon allows up to 4 attachments per post", 400)
	}
	ids := []string{}
	for _, m := range media {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file", m.Name)
		part.Write(m.Data)
		w.Close()
		headers := this.auth(creds)
		headers["Content-Type"] = w.FormDataContentType()
		var out struct {
			ID  string  `json:"id"`
			URL *string `json:"url"`
		}
		if err := doJSON(ctx, "POST", this.base(creds)+"/api/v2/media", headers, &body, &out); err != nil {
			return "", fmt.Errorf("media upload failed: %w", err)
		}
		for i := 0; out.URL == nil && i < 30; i++ { // large media are processed asynchronously
			time.Sleep(time.Second)
			doJSON(ctx, "GET", this.base(creds)+"/api/v1/media/"+out.ID, this.auth(creds), nil, &out)
		}
		ids = append(ids, out.ID)
	}
	headers := this.auth(creds)
	headers["Content-Type"] = "application/json"
	var out struct {
		URL string `json:"url"`
	}
	if err := doJSON(ctx, "POST", this.base(creds)+"/api/v1/statuses", headers,
		jsonBody(map[string]any{"status": text, "media_ids": ids}), &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

func (this Mastodon) Inbox(ctx context.Context, creds map[string]string, limit int) ([]InboxItem, error) {
	var notifs []struct {
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		Account   struct {
			Acct string `json:"acct"`
		} `json:"account"`
		Status *struct {
			Content    string `json:"content"`
			URL        string `json:"url"`
			Visibility string `json:"visibility"`
		} `json:"status"`
	}
	if err := doJSON(ctx, "GET", fmt.Sprintf("%s/api/v1/notifications?limit=%d", this.base(creds), limit), this.auth(creds), nil, &notifs); err != nil {
		return nil, err
	}
	items := []InboxItem{}
	for _, n := range notifs {
		item := InboxItem{Kind: n.Type, From: "@" + n.Account.Acct, Time: n.CreatedAt}
		if n.Status != nil {
			item.Text, item.URL = stripHTML(n.Status.Content), n.Status.URL
			if n.Status.Visibility == "direct" {
				item.Kind = "message"
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// ================================================================ Instagram

// Instagram Graph API: needs a Business or Creator account, an access token
// and the account id. Instagram downloads the media itself so Filestash must
// be reachable from the internet (admin > general > host).
type Instagram struct{}

var instagramGraph = "https://graph.facebook.com/v23.0"

func (this Instagram) Title() string    { return "Instagram" }
func (this Instagram) Fields() []string { return []string{"account_id", "access_token", "graph_url"} }
func (this Instagram) MaxLength() int   { return 2200 }

func (this Instagram) graph(creds map[string]string) string {
	if g := strings.TrimSuffix(creds["graph_url"], "/"); g != "" {
		return g
	}
	return instagramGraph
}

func (this Instagram) call(ctx context.Context, creds map[string]string, method, path string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("access_token", creds["access_token"])
	u := this.graph(creds) + path
	var body io.Reader
	headers := map[string]string{}
	if method == "GET" {
		u += "?" + params.Encode()
	} else {
		body = strings.NewReader(params.Encode())
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	return doJSON(ctx, method, u, headers, body, out)
}

func (this Instagram) Verify(ctx context.Context, creds map[string]string) (string, error) {
	var out struct {
		Username string `json:"username"`
	}
	if err := this.call(ctx, creds, "GET", "/"+creds["account_id"], url.Values{"fields": {"username"}}, &out); err != nil {
		return "", err
	}
	return "@" + out.Username, nil
}

func (this Instagram) container(ctx context.Context, creds map[string]string, params url.Values) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := this.call(ctx, creds, "POST", "/"+creds["account_id"]+"/media", params, &out); err != nil {
		return "", err
	}
	for i := 0; i < 60; i++ { // wait until instagram has fetched and processed the media
		var st struct {
			StatusCode string `json:"status_code"`
		}
		if err := this.call(ctx, creds, "GET", "/"+out.ID, url.Values{"fields": {"status_code"}}, &st); err != nil || st.StatusCode == "" || st.StatusCode == "FINISHED" {
			break
		} else if st.StatusCode == "ERROR" || st.StatusCode == "EXPIRED" {
			return "", fmt.Errorf("instagram could not process the media (%s)", st.StatusCode)
		}
		time.Sleep(2 * time.Second)
	}
	return out.ID, nil
}

func (this Instagram) Post(ctx context.Context, creds map[string]string, text string, media []Media) (string, error) {
	if err := checkLength(this, text); err != nil {
		return "", err
	}
	if len(media) == 0 {
		return "", NewError("Instagram posts need at least one image or video", 400)
	} else if len(media) > 10 {
		return "", NewError("Instagram allows up to 10 items per post", 400)
	}
	item := func(m Media, carousel bool) url.Values {
		p := url.Values{}
		if isImage(m.Name) {
			p.Set("image_url", m.PublicURL)
		} else {
			p.Set("media_type", "REELS")
			if carousel {
				p.Set("media_type", "VIDEO")
			}
			p.Set("video_url", m.PublicURL)
		}
		if carousel {
			p.Set("is_carousel_item", "true")
		}
		return p
	}
	for _, m := range media {
		if m.PublicURL == "" {
			return "", NewError("Instagram needs Filestash to be reachable from the internet: set the host in admin > settings > general", 400)
		}
	}
	var creation string
	var err error
	if len(media) == 1 {
		p := item(media[0], false)
		p.Set("caption", text)
		creation, err = this.container(ctx, creds, p)
	} else {
		children := []string{}
		for _, m := range media {
			id, err := this.container(ctx, creds, item(m, true))
			if err != nil {
				return "", err
			}
			children = append(children, id)
		}
		creation, err = this.container(ctx, creds, url.Values{"media_type": {"CAROUSEL"}, "caption": {text}, "children": {strings.Join(children, ",")}})
	}
	if err != nil {
		return "", err
	}
	var published struct {
		ID string `json:"id"`
	}
	if err := this.call(ctx, creds, "POST", "/"+creds["account_id"]+"/media_publish", url.Values{"creation_id": {creation}}, &published); err != nil {
		return "", err
	}
	var link struct {
		Permalink string `json:"permalink"`
	}
	this.call(ctx, creds, "GET", "/"+published.ID, url.Values{"fields": {"permalink"}}, &link)
	return link.Permalink, nil
}

func (this Instagram) Inbox(ctx context.Context, creds map[string]string, limit int) ([]InboxItem, error) {
	var media struct {
		Data []struct {
			ID            string `json:"id"`
			Caption       string `json:"caption"`
			Permalink     string `json:"permalink"`
			CommentsCount int    `json:"comments_count"`
		} `json:"data"`
	}
	if err := this.call(ctx, creds, "GET", "/"+creds["account_id"]+"/media",
		url.Values{"fields": {"id,caption,permalink,comments_count"}, "limit": {"5"}}, &media); err != nil {
		return nil, err
	}
	items := []InboxItem{}
	for _, m := range media.Data {
		if m.CommentsCount == 0 {
			continue
		}
		var comments struct {
			Data []struct {
				Text      string `json:"text"`
				Username  string `json:"username"`
				Timestamp string `json:"timestamp"`
			} `json:"data"`
		}
		if this.call(ctx, creds, "GET", "/"+m.ID+"/comments", url.Values{"fields": {"text,username,timestamp"}, "limit": {fmt.Sprint(limit)}}, &comments) != nil {
			continue
		}
		for _, c := range comments.Data {
			t, _ := time.Parse("2006-01-02T15:04:05-0700", c.Timestamp)
			items = append(items, InboxItem{Kind: "comment on: " + truncate(m.Caption, 40), From: "@" + c.Username, Text: c.Text, URL: m.Permalink, Time: t})
		}
	}
	return items, nil
}
