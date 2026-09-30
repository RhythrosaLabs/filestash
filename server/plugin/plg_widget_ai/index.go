package plg_widget_ai

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"

	. "github.com/mickael-kerjean/filestash/server/common"
	. "github.com/mickael-kerjean/filestash/server/middleware"

	"github.com/gorilla/mux"
)

//go:embed assets/ai.js
var JS []byte

//go:embed assets/filespage.diff
var PATCH []byte

func init() {
	Hooks.Register.HttpEndpoint(func(r *mux.Router) error {
		mw := []Middleware{ApiHeaders, SecureHeaders, PluginGuard, SessionStart, LoggedInOnly}
		r.HandleFunc("/api/plg_widget_ai/chat", NewMiddlewareChain(chatHandler, mw)).Methods("POST")
		r.HandleFunc("/api/plg_widget_ai/memory", NewMiddlewareChain(listMemoryHandler, mw)).Methods("GET")
		r.HandleFunc("/api/plg_widget_ai/memory", NewMiddlewareChain(deleteMemoryHandler, mw)).Methods("DELETE")
		r.HandleFunc(WithBase("/assets/"+BUILD_REF+"/plugin/plg_widget_ai.js"), func(res http.ResponseWriter, req *http.Request) {
			res.Header().Set("Content-Type", "application/javascript")
			res.Write(JS)
		}).Methods("GET")
		return nil
	})
	Hooks.Register.OnConfig(func() {
		if PluginEnable() {
			Hooks.Register.StaticPatch(PATCH, WithID("plg_widget_ai"))
		} else {
			Hooks.Register.StaticPatch([]byte(""), WithID("plg_widget_ai"))
		}
	})
}

func PluginGuard(fn HandlerFunc) HandlerFunc {
	return func(ctx *App, res http.ResponseWriter, req *http.Request) {
		if !PluginEnable() {
			SendErrorResult(res, ErrNotAllowed)
			return
		}
		fn(ctx, res, req)
	}
}

func getUser(session map[string]string) string {
	if session["username"] != "" {
		return session["username"]
	} else if session["user"] != "" {
		return session["user"]
	}
	return "default"
}

func chatHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	var body Request
	if err := json.NewDecoder(http.MaxBytesReader(res, req.Body, 1<<20)).Decode(&body); err != nil || body.Message == "" {
		SendErrorResult(res, NewError("Invalid request", 400))
		return
	}
	if body.Path == "" {
		body.Path = "/"
	}
	sess := &Session{ctx: ctx, user: getUser(ctx.Session)}
	cwd, _, err := sess.resolve(body.Path, true)
	if err != nil {
		SendErrorResult(res, err)
		return
	}
	sess.cwd = cwd
	llm := LLM{BaseURL: PluginBaseURL(), Model: PluginModel(), APIKey: PluginAPIKey()}
	out, err := runAgent(req.Context(), llm, sess, body, PluginMaxSteps())
	if err != nil {
		Log.Warning("plg_widget_ai::chat err=%s", err.Error())
		out.Reply = "⚠️ " + err.Error()
	}
	SendSuccessResult(res, out)
}

func listMemoryHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	SendSuccessResults(res, memories(getUser(ctx.Session)))
}

func deleteMemoryHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(req.URL.Query().Get("id"), 10, 64)
	if err != nil {
		SendErrorResult(res, ErrNotValid)
		return
	}
	if err := forget(getUser(ctx.Session), id); err != nil {
		SendErrorResult(res, err)
		return
	}
	SendSuccessResult(res, nil)
}
