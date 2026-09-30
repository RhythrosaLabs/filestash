package plg_widget_ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"
)

// OAuthProvider is implemented by networks that connect through a sign in
// page (YouTube) rather than a token pasted in the form
type OAuthProvider interface {
	AuthURL(creds map[string]string, redirect, state string) string
	Exchange(ctx context.Context, creds map[string]string, code, redirect string) error
}

type pendingOAuth struct {
	user, provider, redirect string
	creds                    map[string]string
	created                  time.Time
}

var oauthStates sync.Map // state -> pendingOAuth

func newOAuthState(p pendingOAuth) string {
	b := make([]byte, 24)
	rand.Read(b)
	state := hex.EncodeToString(b)
	p.created = time.Now()
	oauthStates.Store(state, p)
	oauthStates.Range(func(k, v any) bool { // forget abandoned sign ins
		if time.Since(v.(pendingOAuth).created) > 15*time.Minute {
			oauthStates.Delete(k)
		}
		return true
	})
	return state
}

func oauthRedirectURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + req.Host + WithBase("/api/plg_widget_ai/social/oauth/callback")
}

func oauthCallbackHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	page := func(status int, msg string) {
		res.Header().Set("Content-Type", "text/html; charset=utf-8")
		res.WriteHeader(status)
		res.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<body style="font-family:system-ui;max-width:32em;margin:3em auto;padding:0 16px;line-height:1.5">
<p>` + msg + `</p><p>You can close this tab.</p>
<script>try { window.opener && window.opener.postMessage("plg_widget_ai:accounts", location.origin) } catch (e) {}</script>`))
	}
	v, ok := oauthStates.LoadAndDelete(req.URL.Query().Get("state"))
	if !ok {
		page(http.StatusBadRequest, "This sign in link expired, start again from the 🔗 button.")
		return
	}
	p := v.(pendingOAuth)
	if p.user != getUser(ctx.Session) {
		page(http.StatusForbidden, "This sign in was started by another user.")
		return
	}
	if e := req.URL.Query().Get("error"); e != "" {
		page(http.StatusBadRequest, "Sign in cancelled: "+html.EscapeString(e))
		return
	}
	provider := providers[p.provider]
	c, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	if err := provider.(OAuthProvider).Exchange(c, p.creds, req.URL.Query().Get("code"), p.redirect); err != nil {
		page(http.StatusBadGateway, "Couldn't finish the sign in: "+html.EscapeString(err.Error()))
		return
	}
	name, err := provider.Verify(c, p.creds)
	if err != nil {
		page(http.StatusBadGateway, "Signed in, but "+html.EscapeString(err.Error()))
		return
	}
	if _, err := addAccount(p.user, p.provider, name, p.creds); err != nil {
		page(http.StatusInternalServerError, html.EscapeString(err.Error()))
		return
	}
	page(http.StatusOK, "✔ "+html.EscapeString(provider.Title()+" "+name)+" is connected to Filestash.")
}
