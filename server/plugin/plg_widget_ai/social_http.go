package plg_widget_ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"
	. "github.com/mickael-kerjean/filestash/server/middleware"

	"github.com/gorilla/mux"
)

func init() {
	Hooks.Register.HttpEndpoint(func(r *mux.Router) error {
		mw := []Middleware{ApiHeaders, SecureHeaders, SessionStart, LoggedInOnly, PluginGuard}
		r.HandleFunc("/api/plg_widget_ai/social/accounts", NewMiddlewareChain(listAccountsHandler, mw)).Methods("GET")
		r.HandleFunc("/api/plg_widget_ai/social/accounts", NewMiddlewareChain(addAccountHandler, mw)).Methods("POST")
		r.HandleFunc("/api/plg_widget_ai/social/accounts", NewMiddlewareChain(removeAccountHandler, mw)).Methods("DELETE")
		r.HandleFunc("/api/plg_widget_ai/social/queue", NewMiddlewareChain(queueHandler, mw)).Methods("GET")
		r.HandleFunc("/api/plg_widget_ai/social/posts/approve", NewMiddlewareChain(approveHandler, mw)).Methods("POST")
		r.HandleFunc("/api/plg_widget_ai/social/posts/retry", NewMiddlewareChain(retryHandler, mw)).Methods("POST")
		r.HandleFunc("/api/plg_widget_ai/social/posts", NewMiddlewareChain(cancelPostHandler, mw)).Methods("DELETE")
		r.HandleFunc("/api/plg_widget_ai/social/routines", NewMiddlewareChain(deleteRoutineHandler, mw)).Methods("DELETE")
		// public: instagram downloads the media of a post from here. Names are 128 bits random
		// and only the media of posts that are not published yet are served
		r.HandleFunc(WithBase("/api/plg_widget_ai/social/media/{name}"), mediaHandler).Methods("GET", "HEAD")
		return nil
	})
}

func idParam(req *http.Request) int64 {
	id, _ := strconv.ParseInt(req.URL.Query().Get("id"), 10, 64)
	return id
}

func listAccountsHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	fields := map[string][]string{}
	for name, p := range providers {
		fields[name] = p.Fields()
	}
	SendSuccessResult(res, map[string]any{"accounts": listAccounts(getUser(ctx.Session)), "providers": fields})
}

func addAccountHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	var body struct {
		Provider string            `json:"provider"`
		Creds    map[string]string `json:"creds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(res, req.Body, 64<<10)).Decode(&body); err != nil {
		SendErrorResult(res, ErrNotValid)
		return
	}
	provider, ok := providers[body.Provider]
	if !ok {
		SendErrorResult(res, NewError("Unknown provider", 400))
		return
	}
	creds := map[string]string{}
	for _, f := range provider.Fields() {
		creds[f] = strings.TrimSpace(body.Creds[f])
	}
	c, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	name, err := provider.Verify(c, creds)
	if err != nil {
		SendErrorResult(res, NewError("Cannot connect to "+provider.Title()+": "+err.Error(), 400))
		return
	}
	id, err := addAccount(getUser(ctx.Session), body.Provider, name, creds)
	if err != nil {
		SendErrorResult(res, err)
		return
	}
	SendSuccessResult(res, Account{ID: id, Provider: body.Provider, Name: name})
}

func removeAccountHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	if err := removeAccount(getUser(ctx.Session), idParam(req)); err != nil {
		SendErrorResult(res, err)
		return
	}
	SendSuccessResult(res, nil)
}

func queueHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	user := getUser(ctx.Session)
	SendSuccessResult(res, map[string]any{"posts": listPosts(user, 50), "routines": listRoutines(user)})
}

func approveHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	user := getUser(ctx.Session)
	id := idParam(req)
	if err := approvePost(user, id); err != nil {
		SendErrorResult(res, err)
		return
	}
	post, err := getPost(user, id)
	if err != nil {
		SendErrorResult(res, err)
		return
	}
	if post.ScheduledAt > time.Now().Unix() {
		SendSuccessResult(res, post)
		return
	}
	c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := publish(c, post); err != nil {
		SendErrorResult(res, NewError("Publishing failed: "+err.Error(), 502))
		return
	}
	post, _ = getPost(user, id)
	SendSuccessResult(res, post)
}

func retryHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	user := getUser(ctx.Session)
	id := idParam(req)
	r, err := db.Exec(`UPDATE social_posts SET status = ?, error = '' WHERE user = ? AND id = ? AND status = ?`, StatusDraft, user, id, StatusFailed)
	if err != nil {
		SendErrorResult(res, err)
		return
	} else if n, _ := r.RowsAffected(); n == 0 {
		SendErrorResult(res, NewError("only failed posts can be retried", 409))
		return
	}
	approveHandler(ctx, res, req)
}

func cancelPostHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	if err := cancelPost(getUser(ctx.Session), idParam(req)); err != nil {
		SendErrorResult(res, err)
		return
	}
	SendSuccessResult(res, nil)
}

func deleteRoutineHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	if err := deleteRoutine(getUser(ctx.Session), idParam(req)); err != nil {
		SendErrorResult(res, err)
		return
	}
	SendSuccessResult(res, nil)
}

func mediaHandler(res http.ResponseWriter, req *http.Request) {
	name := mux.Vars(req)["name"]
	if db == nil || !PluginEnable() || !mediaNameRegex.MatchString(name) {
		http.NotFound(res, req)
		return
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM social_posts WHERE status IN (?, ?, ?) AND media LIKE ?`,
		StatusDraft, StatusScheduled, StatusPosting, `%"`+name+`"%`).Scan(&n)
	if n == 0 {
		http.NotFound(res, req)
		return
	}
	data, err := readMedia(name)
	if err != nil {
		http.NotFound(res, req)
		return
	}
	res.Header().Set("Content-Type", mimeOf(name))
	res.Header().Set("Content-Length", strconv.Itoa(len(data)))
	res.Header().Set("Cache-Control", "no-store")
	if req.Method == "HEAD" {
		return
	}
	res.Write(data)
}
