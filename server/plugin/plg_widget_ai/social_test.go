package plg_widget_ai

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"
	"github.com/mickael-kerjean/filestash/server/pkg/env"
)

func setupSocial(t *testing.T) {
	t.Helper()
	env.SECRET_KEY_DERIVATE_FOR_USER = "0123456789abcdef"
	var err error
	if db, err = sql.Open("sqlite3", filepath.Join(t.TempDir(), "ai.db")); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	if err = initSocialSchema(db); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mediaDir = func() string { return dir }
}

func pngBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		img.Set(x, x, color.RGBA{255, 0, 0, 255})
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// fakeSocial emulates the bits of the Bluesky, Mastodon and Instagram APIs we use
type fakeSocial struct {
	mu      sync.Mutex
	records []map[string]any
	blobs   int
	toots   []map[string]any
	igMedia []string
	igPub   int
}

func (this *fakeSocial) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	this.mu.Lock()
	defer this.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	send := func(v any) { json.NewEncoder(w).Encode(v) }
	form, _ := url.ParseQuery(string(body))
	auth := r.Header.Get("Authorization")
	switch {
	// bluesky
	case r.URL.Path == "/xrpc/com.atproto.server.createSession":
		var in map[string]string
		json.Unmarshal(body, &in)
		if in["password"] != "app-pass" {
			w.WriteHeader(401)
			send(map[string]string{"error": "AuthenticationRequired", "message": "Invalid identifier or password"})
			return
		}
		send(map[string]string{"accessJwt": "jwt", "did": "did:plc:me", "handle": "me.bsky.social"})
	case strings.HasPrefix(r.URL.Path, "/xrpc/") && auth != "Bearer jwt":
		w.WriteHeader(401)
	case r.URL.Path == "/xrpc/com.atproto.repo.uploadBlob":
		this.blobs++
		send(map[string]any{"blob": map[string]any{"$type": "blob", "ref": map[string]string{"$link": "bafy"}, "mimeType": r.Header.Get("Content-Type"), "size": len(body)}})
	case r.URL.Path == "/xrpc/com.atproto.repo.createRecord":
		var in map[string]any
		json.Unmarshal(body, &in)
		this.records = append(this.records, in["record"].(map[string]any))
		send(map[string]string{"uri": "at://did:plc:me/app.bsky.feed.post/3kabc", "cid": "x"})
	case r.URL.Path == "/xrpc/app.bsky.notification.listNotifications":
		send(map[string]any{"notifications": []any{map[string]any{"reason": "reply", "indexedAt": "2026-09-30T10:00:00Z", "author": map[string]string{"handle": "fan.bsky.social"}, "record": map[string]string{"text": "love it!"}}}})
	case r.URL.Path == "/xrpc/chat.bsky.convo.listConvos":
		if r.Header.Get("atproto-proxy") == "" {
			w.WriteHeader(400)
			return
		}
		send(map[string]any{"convos": []any{map[string]any{"unreadCount": 2, "members": []any{map[string]string{"handle": "me.bsky.social"}, map[string]string{"handle": "friend.bsky.social"}}, "lastMessage": map[string]any{"text": "coffee tomorrow?", "sentAt": "2026-09-30T11:00:00Z"}}}})
	// mastodon
	case strings.HasPrefix(r.URL.Path, "/api/") && auth != "Bearer masto-token":
		w.WriteHeader(401)
		send(map[string]string{"error": "The access token is invalid"})
	case r.URL.Path == "/api/v1/accounts/verify_credentials":
		send(map[string]string{"acct": "me"})
	case r.URL.Path == "/api/v2/media":
		send(map[string]any{"id": "m1", "url": "https://x/m1"})
	case r.URL.Path == "/api/v1/statuses":
		var in map[string]any
		json.Unmarshal(body, &in)
		this.toots = append(this.toots, in)
		send(map[string]string{"url": "https://masto.test/@me/1"})
	case r.URL.Path == "/api/v1/notifications":
		send([]any{map[string]any{"type": "mention", "created_at": "2026-09-30T10:00:00Z", "account": map[string]string{"acct": "bob"}, "status": map[string]string{"content": "<p>hey <b>you</b></p>", "url": "https://masto.test/@bob/2", "visibility": "direct"}}})
	// instagram
	case r.URL.Query().Get("access_token") != "ig-token" && form.Get("access_token") != "ig-token" && strings.HasPrefix(r.URL.Path, "/ig"):
		w.WriteHeader(400)
		send(map[string]any{"error": map[string]string{"message": "Invalid OAuth access token"}})
	case r.URL.Path == "/ig/1784" && r.Method == "GET":
		send(map[string]string{"username": "my_ig"})
	case r.URL.Path == "/ig/1784/media" && r.Method == "POST":
		this.igMedia = append(this.igMedia, form.Get("image_url")+"|"+form.Get("caption")+"|"+form.Get("media_type"))
		send(map[string]string{"id": "c1"})
	case r.URL.Path == "/ig/c1":
		send(map[string]string{"status_code": "FINISHED"})
	case r.URL.Path == "/ig/1784/media_publish":
		this.igPub++
		send(map[string]string{"id": "p1"})
	case r.URL.Path == "/ig/p1":
		send(map[string]string{"permalink": "https://instagram.com/p/abc"})
	default:
		w.WriteHeader(404)
	}
}

func TestSocialProviders(t *testing.T) {
	setupSocial(t)
	fake := &fakeSocial{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	ctx := context.Background()

	bsky := map[string]string{"handle": "@me.bsky.social", "app_password": "app-pass", "service": srv.URL}
	if name, err := (Bluesky{}).Verify(ctx, bsky); err != nil || name != "@me.bsky.social" {
		t.Fatalf("verify: %q %v", name, err)
	}
	if _, err := (Bluesky{}).Verify(ctx, map[string]string{"handle": "me", "app_password": "bad", "service": srv.URL}); err == nil || !strings.Contains(err.Error(), "Invalid identifier") {
		t.Fatalf("expected a readable auth error, got %v", err)
	}
	link, err := (Bluesky{}).Post(ctx, bsky, "new drop 🎨 https://example.org/shop", []Media{{Name: "a.png", Data: pngBytes()}})
	if err != nil || link != "https://bsky.app/profile/me.bsky.social/post/3kabc" {
		t.Fatalf("post: %q %v", link, err)
	}
	rec := fake.records[0]
	facets := rec["facets"].([]any)
	idx := facets[0].(map[string]any)["index"].(map[string]any)
	if text := rec["text"].(string); text[int(idx["byteStart"].(float64)):int(idx["byteEnd"].(float64))] != "https://example.org/shop" {
		t.Errorf("wrong link facet: %v", idx)
	}
	if rec["embed"] == nil || fake.blobs != 1 {
		t.Errorf("image not attached")
	}
	if _, err := (Bluesky{}).Post(ctx, bsky, strings.Repeat("a", 301), nil); err == nil {
		t.Errorf("expected length error")
	}
	items, err := (Bluesky{}).Inbox(ctx, bsky, 10)
	if err != nil || len(items) != 2 || items[1].From != "@friend.bsky.social" || !strings.HasPrefix(items[1].Kind, "message") {
		t.Fatalf("inbox: %+v %v", items, err)
	}

	masto := map[string]string{"instance": srv.URL, "access_token": "masto-token"}
	if _, err := (Mastodon{}).Post(ctx, masto, "hello fediverse", []Media{{Name: "a.png", Data: pngBytes()}}); err != nil {
		t.Fatal(err)
	}
	if ids := fake.toots[0]["media_ids"].([]any); len(ids) != 1 {
		t.Errorf("media not attached: %v", fake.toots[0])
	}
	items, err = (Mastodon{}).Inbox(ctx, masto, 10)
	if err != nil || items[0].Kind != "message" || items[0].Text != "hey you" {
		t.Fatalf("mastodon inbox: %+v %v", items, err)
	}
	if _, err := (Mastodon{}).Verify(ctx, map[string]string{"instance": srv.URL, "access_token": "bad"}); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected auth error, got %v", err)
	}

	ig := map[string]string{"account_id": "1784", "access_token": "ig-token", "graph_url": srv.URL + "/ig"}
	if name, err := (Instagram{}).Verify(ctx, ig); err != nil || name != "@my_ig" {
		t.Fatalf("ig verify: %q %v", name, err)
	}
	if _, err := (Instagram{}).Post(ctx, ig, "caption", []Media{{Name: "a.jpg", Data: []byte("x")}}); err == nil {
		t.Errorf("expected an error without public url")
	}
	link, err = (Instagram{}).Post(ctx, ig, "caption #art", []Media{{Name: "a.jpg", PublicURL: "https://files.me/a.jpg"}})
	if err != nil || link != "https://instagram.com/p/abc" || fake.igPub != 1 || fake.igMedia[0] != "https://files.me/a.jpg|caption #art|" {
		t.Fatalf("ig post: %q %v %v", link, err, fake.igMedia)
	}
}

func TestCron(t *testing.T) {
	mon18 := time.Date(2026, 10, 5, 18, 0, 0, 0, time.Local) // a monday
	for expr, want := range map[string]bool{
		"0 18 * * 1": true, "0 18 * * 1,4": true, "0 18 * * 2": false, "*/15 * * * *": true,
		"0 9-17 * * *": false, "0 18 5 10 *": true, "@daily": false, "0 18 * * 1-5": true,
	} {
		if got := cronMatch(expr, mon18); got != want {
			t.Errorf("cronMatch(%q) = %v, want %v", expr, got, want)
		}
	}
	sun := time.Date(2026, 10, 4, 9, 0, 0, 0, time.Local)
	if !cronMatch("0 9 * * 7", sun) || !cronMatch("@weekly", mon18.Add(-9*time.Hour)) {
		t.Errorf("sunday as 7 or @weekly not matched")
	}
	for _, bad := range []string{"", "* * *", "61 * * * *", "0 25 * * *", "a b c d e"} {
		if _, err := normalizeCron(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestSocialFlow(t *testing.T) {
	setupSocial(t)
	fake := &fakeSocial{}
	social := httptest.NewServer(fake)
	defer social.Close()

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "art"), 0755)
	os.WriteFile(filepath.Join(root, "art", "01 sunset.png"), pngBytes(), 0644)
	os.WriteFile(filepath.Join(root, "art", "02 notes.txt"), []byte("my new series about the sea"), 0644)
	os.WriteFile(filepath.Join(root, "art", "03 archive.zip"), []byte("zip"), 0644)

	accountID, _ := addAccount("me", "bluesky", "@me.bsky.social", map[string]string{"handle": "me", "app_password": "app-pass", "service": social.URL})
	addAccount("other", "bluesky", "@other", map[string]string{})

	// the model: first creates a post with an image, then a routine
	llm := &fakeLLM{script: []Message{
		call("1", "create_post", `{"account":"bluesky","text":"sunset vibes","media":["/art/01 sunset.png"]}`),
		call("2", "create_post", `{"account":"bluesky","text":"later","when":"2999-01-01 10:00"}`),
		call("3", "create_routine", `{"account":"@me.bsky.social","folder":"/art","instructions":"short, add #art","schedule":"0 18 * * 1","review":false}`),
		call("4", "create_post", `{"account":"@other","text":"not mine"}`),
		call("5", "social_inbox", `{}`),
	}}
	srv := httptest.NewServer(llm)
	defer srv.Close()
	app := &App{Backend: dirBackend{root}, Session: map[string]string{"username": "me"}, Context: context.Background()}
	sess := &Session{ctx: app, user: "me", cwd: "/", token: "tok"}
	res, err := runAgent(context.Background(), LLM{BaseURL: srv.URL}, sess, Request{Message: "post my sunset and set up weekly posts"}, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drafts) != 2 || res.Drafts[0].Status != StatusDraft || len(res.Drafts[0].Media) != 1 {
		t.Fatalf("expected 2 drafts: %+v", res.Drafts)
	}
	toolOut := func(id string) string {
		for _, m := range llm.requests[len(llm.requests)-1] {
			if m.Role == "tool" && m.ToolCallID == id {
				return m.Content
			}
		}
		return ""
	}
	if out := toolOut("4"); !strings.HasPrefix(out, "error") {
		t.Errorf("must not reach another user's account: %s", out)
	}
	if out := toolOut("5"); !strings.Contains(out, "love it!") || !strings.Contains(out, "coffee tomorrow?") {
		t.Errorf("inbox: %s", out)
	}
	if len(fake.records) != 0 {
		t.Fatal("nothing must be published before approval")
	}

	// scheduler doesn't touch drafts, approving publishes
	socialTick(time.Now())
	time.Sleep(100 * time.Millisecond)
	if len(fake.records) != 0 {
		t.Fatal("draft published without approval")
	}
	if err := approvePost("other", res.Drafts[0].ID); err == nil {
		t.Fatal("another user approved my post")
	}
	if err := approvePost("me", res.Drafts[0].ID); err != nil {
		t.Fatal(err)
	}
	p, _ := getPost("me", res.Drafts[0].ID)
	if url, err := publish(context.Background(), p); err != nil || url == "" {
		t.Fatalf("publish: %v", err)
	}
	if _, err := publish(context.Background(), p); err == nil {
		t.Fatal("a post must only be published once")
	}
	if p, _ = getPost("me", p.ID); p.Status != StatusPosted || fake.blobs != 1 {
		t.Fatalf("unexpected state %+v", p)
	}
	if _, err := readMedia(p.Media[0]); err == nil {
		t.Error("staged media should be cleaned up after publishing")
	}

	// scheduled in the future: approved but waits
	approvePost("me", res.Drafts[1].ID)
	socialTick(time.Now())
	time.Sleep(100 * time.Millisecond)
	if p, _ = getPost("me", res.Drafts[1].ID); p.Status != StatusScheduled {
		t.Fatalf("future post should stay scheduled: %s", p.Status)
	}
	if err := cancelPost("me", p.ID); err != nil {
		t.Fatal(err)
	}

	// routine: runs on schedule with the stored session, picks files in order and skips unsupported ones
	routines := listRoutines("me")
	if len(routines) != 1 || routines[0].Cron != "0 18 * * 1" || routines[0].AccountID != accountID {
		t.Fatalf("routine: %+v", routines)
	}
	openRoutineApp = func(token string) (*App, error) {
		if token != "tok" {
			t.Fatalf("unexpected token %q", token)
		}
		return app, nil
	}
	var captionPrompts []string
	llm.script, llm.requests = nil, nil
	vision := &captionLLM{visionFails: true}
	srv2 := httptest.NewServer(vision)
	defer srv2.Close()
	Config.Get("features.ai.base_url").Set(srv2.URL)
	Config.Get("features.ai.model").Set("test")

	socialTick(time.Date(2026, 10, 5, 17, 0, 0, 0, time.Local)) // not the time yet
	time.Sleep(100 * time.Millisecond)
	if len(vision.prompts) != 0 {
		t.Fatal("routine ran outside of its schedule")
	}
	msg, err := runRoutine(context.Background(), listRoutines("me")[0])
	if err != nil || !strings.Contains(msg, "01 sunset.png") {
		t.Fatalf("routine run 1: %q %v", msg, err)
	}
	if len(vision.prompts) != 2 || !vision.sawImage {
		t.Fatalf("expected a vision attempt then a text fallback, got %d calls", len(vision.prompts))
	}
	msg, err = runRoutine(context.Background(), listRoutines("me")[0])
	if err != nil || !strings.Contains(msg, "02 notes.txt") {
		t.Fatalf("routine run 2: %q %v", msg, err)
	}
	captionPrompts = vision.prompts
	if last := captionPrompts[len(captionPrompts)-1]; !strings.Contains(last, "my new series about the sea") || !strings.Contains(last, "#art") {
		t.Fatalf("caption prompt misses the file content or instructions: %s", last)
	}
	msg, _ = runRoutine(context.Background(), listRoutines("me")[0])
	if !strings.Contains(msg, "nothing new") {
		t.Fatalf("zip must be skipped and nothing left: %q", msg)
	}
	if len(fake.records) != 3 {
		t.Fatalf("expected 3 posts published, got %d", len(fake.records))
	}
	if !strings.Contains(fake.records[1]["text"].(string), "generated caption") {
		t.Errorf("unexpected caption %v", fake.records[1]["text"])
	}

	// removing an account removes its routines
	removeAccount("me", accountID)
	if len(listRoutines("me")) != 0 {
		t.Fatal("routines of a removed account must be deleted")
	}
}

// captionLLM answers completions, failing when an image is sent to emulate a text only model
type captionLLM struct {
	visionFails bool
	sawImage    bool
	prompts     []string
}

func (this *captionLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	user := body.Messages[len(body.Messages)-1].Content
	if parts, ok := user.([]any); ok {
		this.sawImage = true
		this.prompts = append(this.prompts, parts[0].(map[string]any)["text"].(string))
		if this.visionFails {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"model does not support images"}`))
			return
		}
	} else {
		this.prompts = append(this.prompts, user.(string))
	}
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "<think>hmm</think>\"generated caption #art\""}}}})
}
