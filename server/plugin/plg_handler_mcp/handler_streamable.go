package plg_handler_mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	. "github.com/mickael-kerjean/filestash/server/pkg/core"
	. "github.com/mickael-kerjean/filestash/server/pkg/files"
	. "github.com/mickael-kerjean/filestash/server/plugin/plg_handler_mcp/types"
	. "github.com/mickael-kerjean/filestash/server/plugin/plg_handler_mcp/utils"

	"github.com/google/uuid"
)

// Streamable HTTP transport (MCP 2025-03-26 and later) on POST /mcp, next to
// the legacy SSE transport. Agents that only speak the new transport, like
// DeepSeek Harness, connect here with the same bearer tokens.

var supportedProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

type streamSession struct {
	mu       sync.Mutex
	session  UserSession
	lastUsed time.Time
}

var (
	streamSessions sync.Map // Mcp-Session-Id -> *streamSession
	streamGC       sync.Once
)

func (this *Server) streamableHandler(_ *App, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
	switch r.Method {
	case http.MethodGet: // no server initiated stream
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		if s, ok := streamSessions.Load(r.Header.Get("Mcp-Session-Id")); ok && s.(*streamSession).session.Token == ExtractToken(r) {
			streamSessions.Delete(r.Header.Get("Mcp-Session-Id"))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	streamGC.Do(func() {
		go func() {
			for range time.Tick(10 * time.Minute) {
				streamSessions.Range(func(k, v any) bool {
					if time.Since(v.(*streamSession).lastUsed) > time.Hour {
						streamSessions.Delete(k)
					}
					return true
				})
			}
		}()
	})

	token := ExtractToken(r)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Www-Authenticate", "Bearer resource_metadata=\""+this.baseURL(r)+"/.well-known/oauth-protected-resource\"")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", Error: &JSONRPCError{Code: http.StatusUnauthorized, Message: "Missing or invalid access token"}})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if b := bytes.TrimSpace(body); len(b) > 0 && b[0] == '[' {
		writeJSONError(w, http.StatusBadRequest, 0, "JSON-RPC batches are not supported")
		return
	}
	var raw struct {
		ID     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
	}
	request := JSONRPCRequest{}
	if json.Unmarshal(body, &raw) != nil || json.Unmarshal(body, &request) != nil {
		writeJSONError(w, http.StatusBadRequest, 0, "Parse error")
		return
	}
	if raw.ID == nil || raw.Method == "" { // notification or response from the client
		w.WriteHeader(http.StatusAccepted)
		return
	}

	backend, err := getBackend(token)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, request.ID, "You aren't authenticated")
		return
	}

	var s *streamSession
	sid := r.Header.Get("Mcp-Session-Id")
	if request.Method == "initialize" {
		sid = uuid.New().String()
		s = &streamSession{session: UserSession{Id: sid, Token: token, HomeDir: "/", CurrDir: "/", Ping: Ping{LastResponse: time.Now()}}}
		if home, err := GetHome(backend, "/"); err == nil {
			s.session.HomeDir = home
			s.session.CurrDir = ToString(home, "/")
		}
		streamSessions.Store(sid, s)
	} else if sid == "" { // stateless client
		s = &streamSession{session: UserSession{Token: token, HomeDir: "/", CurrDir: "/"}}
	} else if v, ok := streamSessions.Load(sid); ok && v.(*streamSession).session.Token == token {
		s = v.(*streamSession)
	} else {
		writeJSONError(w, http.StatusNotFound, request.ID, "Session not found, initialize again")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	s.session.Backend = backend

	if sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if request.Method == "initialize" {
		version := "2025-03-26"
		if asked, ok := request.Params["protocolVersion"].(string); ok {
			for _, v := range supportedProtocolVersions {
				if v == asked {
					version = asked
				}
			}
		}
		SendMessage(w, request.ID, InitializeResponse{
			ProtocolVersion: version,
			ServerInfo:      ServerInfo{Name: "Filestash", Version: "1.0.0"},
			Capabilities: Capabilities{
				Tools:     map[string]interface{}{},
				Resources: map[string]interface{}{},
				Prompts:   map[string]interface{}{},
			},
		})
		return
	}
	this.dispatch(w, request, &s.session)
}

func writeJSONError(w http.ResponseWriter, status int, id uint64, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &JSONRPCError{Code: status, Message: msg}})
}
