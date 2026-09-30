package plg_widget_ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	. "github.com/mickael-kerjean/filestash/server/common"
)

// fakeGoogle emulates Google's OAuth token endpoint and the parts of the YouTube Data API we use
type fakeGoogle struct {
	mu       sync.Mutex
	meta     map[string]any
	uploaded []byte
	declared string
	srv      *httptest.Server
}

func (this *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	this.mu.Lock()
	defer this.mu.Unlock()
	send := func(v any) { json.NewEncoder(w).Encode(v) }
	auth := r.Header.Get("Authorization")
	switch {
	case r.URL.Path == "/token":
		r.ParseForm()
		if r.Form.Get("client_secret") != "shh" {
			w.WriteHeader(401)
			send(map[string]string{"error": "invalid_client"})
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "good-code" || !strings.HasSuffix(r.Form.Get("redirect_uri"), "/api/plg_widget_ai/social/oauth/callback") {
				w.WriteHeader(400)
				send(map[string]string{"error": "invalid_grant"})
				return
			}
			send(map[string]any{"access_token": "at", "refresh_token": "rt-1", "expires_in": 3599})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "rt-1" {
				w.WriteHeader(400)
				send(map[string]string{"error": "invalid_grant"})
				return
			}
			send(map[string]any{"access_token": "at", "expires_in": 3599})
		}
	case auth != "Bearer at":
		w.WriteHeader(401)
		send(map[string]any{"error": map[string]string{"message": "Invalid Credentials"}})
	case r.URL.Path == "/yt/channels" && r.URL.Query().Get("mine") == "true":
		send(map[string]any{"items": []any{map[string]any{"id": "UC1", "snippet": map[string]string{"title": "My Channel", "customUrl": "@mychannel"}, "contentDetails": map[string]any{"relatedPlaylists": map[string]string{"uploads": "UU1"}}}}})
	case r.URL.Path == "/yt/playlistItems" && r.URL.Query().Get("playlistId") == "UU1":
		send(map[string]any{"items": []any{map[string]any{"snippet": map[string]string{"title": "Studio tour"}, "contentDetails": map[string]string{"videoId": "vid1"}}}})
	case r.URL.Path == "/yt/commentThreads" && r.URL.Query().Get("videoId") == "vid1":
		send(map[string]any{"items": []any{map[string]any{"id": "c1", "snippet": map[string]any{"topLevelComment": map[string]any{"snippet": map[string]string{"authorDisplayName": "Fan", "textOriginal": "when is the next one?", "publishedAt": "2026-09-30T10:00:00Z"}}}}}})
	case r.URL.Path == "/upload" && r.Method == "POST":
		if r.URL.Query().Get("uploadType") != "resumable" {
			w.WriteHeader(400)
			return
		}
		json.NewDecoder(r.Body).Decode(&this.meta)
		this.declared = r.Header.Get("X-Upload-Content-Length")
		w.Header().Set("Location", this.srv.URL+"/upload-session/xyz")
		w.WriteHeader(200)
	case r.URL.Path == "/upload-session/xyz" && r.Method == "PUT":
		this.uploaded, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
		send(map[string]string{"id": "newvid"})
	default:
		w.WriteHeader(404)
	}
}

func setupGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{}
	g.srv = httptest.NewServer(g)
	t.Cleanup(g.srv.Close)
	googleAuthURL, googleTokenURL = g.srv.URL+"/auth", g.srv.URL+"/token"
	youtubeAPI, youtubeUploadURL = g.srv.URL+"/yt", g.srv.URL+"/upload"
	youtubeTokens = sync.Map{}
	return g
}

func TestYouTubeProvider(t *testing.T) {
	setupSocial(t)
	g := setupGoogle(t)
	ctx := context.Background()
	creds := map[string]string{"client_id": "cid", "client_secret": "shh"}

	u, _ := url.Parse(YouTube{}.AuthURL(creds, "http://localhost:8334/api/plg_widget_ai/social/oauth/callback", "st"))
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("access_type") != "offline" || q.Get("state") != "st" || !strings.Contains(q.Get("scope"), "youtube.upload") {
		t.Fatalf("bad auth url %s", u)
	}
	if err := (YouTube{}).Exchange(ctx, creds, "bad-code", "http://localhost:8334/api/plg_widget_ai/social/oauth/callback"); err == nil {
		t.Fatal("a bad code must fail")
	}
	if err := (YouTube{}).Exchange(ctx, creds, "good-code", "http://localhost:8334/api/plg_widget_ai/social/oauth/callback"); err != nil || creds["refresh_token"] != "rt-1" {
		t.Fatalf("exchange: %v %v", err, creds)
	}
	if name, err := (YouTube{}).Verify(ctx, creds); err != nil || name != "@mychannel" {
		t.Fatalf("verify: %q %v", name, err)
	}

	video := bytes.Repeat([]byte("frame"), 100000) // 500KB
	staged, err := stageReader("clip.mp4", bytes.NewReader(video), maxVideoSize)
	if err != nil {
		t.Fatal(err)
	}
	link, err := (YouTube{}).Post(ctx, creds, "My <best> video\n\nBehind the scenes #art", []Media{{Name: staged, Path: mediaPath(staged)}})
	if err != nil || link != "https://youtu.be/newvid" {
		t.Fatalf("post: %q %v", link, err)
	}
	snippet := g.meta["snippet"].(map[string]any)
	status := g.meta["status"].(map[string]any)
	if snippet["title"] != "My best video" || snippet["description"] != "Behind the scenes #art" || status["privacyStatus"] != "private" {
		t.Errorf("unexpected metadata %v", g.meta)
	}
	if !bytes.Equal(g.uploaded, video) || g.declared != "500000" {
		t.Errorf("upload mismatch: %d bytes, declared %s", len(g.uploaded), g.declared)
	}
	if _, err := (YouTube{}).Post(ctx, creds, "title", []Media{{Name: "a.png", Data: pngBytes()}}); err == nil {
		t.Error("images must be refused")
	}

	items, err := (YouTube{}).Inbox(ctx, creds, 10)
	if err != nil || len(items) != 1 || items[0].From != "Fan" || !strings.Contains(items[0].URL, "vid1") {
		t.Fatalf("inbox: %+v %v", items, err)
	}

	youtubeTokens = sync.Map{}
	if _, err := (YouTube{}).Verify(ctx, map[string]string{"client_id": "cid", "client_secret": "shh", "refresh_token": "revoked"}); err == nil || !strings.Contains(err.Error(), "connect the account again") {
		t.Errorf("a revoked token needs a clear message, got %v", err)
	}

	if _, err := stageReader("big.mp4", bytes.NewReader(video), 1000); err == nil {
		t.Error("staging must enforce the size limit")
	}
	if entries, _ := os.ReadDir(mediaDir()); len(entries) != 1 {
		t.Errorf("a refused file must not stay in the staging area, found %d files", len(entries))
	}
}

func TestYouTubeOAuthFlowAndPost(t *testing.T) {
	setupSocial(t)
	g := setupGoogle(t)
	ctx := &App{Session: map[string]string{"username": "me"}, Context: context.Background()}

	// 1. the form posts the client credentials, we answer with google's sign in page
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://localhost:8334/api/plg_widget_ai/social/accounts", strings.NewReader(`{"provider":"youtube","creds":{"client_id":"cid","client_secret":"shh","privacy":"unlisted"}}`))
	addAccountHandler(ctx, rec, req)
	var out struct {
		Result struct {
			AuthURL     string `json:"auth_url"`
			RedirectURI string `json:"redirect_uri"`
		} `json:"result"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Result.RedirectURI != "http://localhost:8334/api/plg_widget_ai/social/oauth/callback" {
		t.Fatalf("unexpected answer %s", rec.Body.String())
	}
	u, _ := url.Parse(out.Result.AuthURL)
	state := u.Query().Get("state")
	if len(listAccounts("me")) != 0 {
		t.Fatal("nothing is stored before the sign in")
	}

	// 2. someone else can't finish my sign in, and a state is single use
	callback := func(user, state, code string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		appCtx := &App{Session: map[string]string{"username": user}, Context: context.Background()}
		oauthCallbackHandler(appCtx, rec, httptest.NewRequest("GET", "http://localhost:8334/api/plg_widget_ai/social/oauth/callback?state="+state+"&code="+code, nil))
		return rec
	}
	if rec := callback("mallory", state, "good-code"); rec.Code != 403 {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	addAccountHandler(ctx, rec, httptest.NewRequest("POST", "http://localhost:8334/x", strings.NewReader(`{"provider":"youtube","creds":{"client_id":"cid","client_secret":"shh","privacy":"unlisted"}}`)))
	json.Unmarshal(rec.Body.Bytes(), &out)
	u, _ = url.Parse(out.Result.AuthURL)
	state = u.Query().Get("state")

	// 3. google redirects back with a code
	if rec := callback("me", state, "good-code"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "@mychannel is connected") {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	if rec := callback("me", state, "good-code"); rec.Code != 400 {
		t.Fatalf("a state must only work once, got %d", rec.Code)
	}
	accounts := listAccounts("me")
	if len(accounts) != 1 || accounts[0].Provider != "youtube" || accounts[0].Name != "@mychannel" {
		t.Fatalf("accounts: %+v", accounts)
	}

	// 4. the assistant drafts a video post from a file of the storage, the user approves, it uploads
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "tour.mp4"), []byte("fake mp4 bytes"), 0644)
	os.WriteFile(filepath.Join(root, "cover.png"), pngBytes(), 0644)
	sess := &Session{ctx: &App{Backend: dirBackend{root}, Session: map[string]string{"username": "me"}, Context: context.Background()}, user: "me", cwd: "/"}
	if _, err := extraTools["create_post"](sess, map[string]any{"account": "youtube", "text": "Studio tour", "media": []any{"/cover.png"}}); err == nil {
		t.Fatal("youtube needs a video")
	}
	if _, err := extraTools["create_post"](sess, map[string]any{"account": "youtube", "text": "Studio tour\nA look around", "media": []any{"/tour.mp4"}}); err != nil {
		t.Fatal(err)
	}
	draft := sess.Drafts[0]
	if err := approvePost("me", draft.ID); err != nil {
		t.Fatal(err)
	}
	p, _ := getPost("me", draft.ID)
	link, err := publish(context.Background(), p)
	if err != nil || link != "https://youtu.be/newvid" || string(g.uploaded) != "fake mp4 bytes" {
		t.Fatalf("publish: %q %v %q", link, err, g.uploaded)
	}
	if g.meta["status"].(map[string]any)["privacyStatus"] != "unlisted" {
		t.Errorf("privacy setting ignored: %v", g.meta["status"])
	}

	// 5. a routine on a folder with a text, an image and a video only picks the video for youtube
	os.WriteFile(filepath.Join(root, "a-notes.txt"), []byte("notes"), 0644)
	llm := &captionLLM{}
	srv := httptest.NewServer(llm)
	defer srv.Close()
	Config.Get("features.ai.base_url").Set(srv.URL)
	openRoutineApp = func(string) (*App, error) { return sess.ctx, nil }
	id, err := createRoutine(Routine{user: "me", AccountID: accounts[0].ID, Folder: "/", Cron: "@daily", token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	var r Routine
	for _, x := range listRoutines("me") {
		if x.ID == id {
			r = x
		}
	}
	msg, err := runRoutine(context.Background(), r)
	if err != nil || !strings.Contains(msg, "tour.mp4") {
		t.Fatalf("routine: %q %v", msg, err)
	}
	if last := llm.prompts[len(llm.prompts)-1]; !strings.Contains(last, "the first line is the video title") {
		t.Errorf("youtube caption format not requested: %s", last)
	}
	if msg, _ := runRoutine(context.Background(), listRoutines("me")[0]); !strings.Contains(msg, "nothing new") {
		t.Errorf("images and text must be skipped for youtube: %q", msg)
	}
}
