package plg_widget_ai

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	. "github.com/mickael-kerjean/filestash/server/common"
	_ "github.com/mickael-kerjean/filestash/server/pkg/sqlite"
)

// dirBackend is a minimal storage over a local directory
type dirBackend struct{ root string }

func (this dirBackend) p(path string) string                           { return filepath.Join(this.root, path) }
func (this dirBackend) Init(map[string]string, *App) (IBackend, error) { return this, nil }
func (this dirBackend) LoginForm() Form                                { return Form{} }
func (this dirBackend) Ls(path string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(this.p(path))
	if err != nil {
		return nil, err
	}
	out := []os.FileInfo{}
	for _, e := range entries {
		info, _ := e.Info()
		out = append(out, info)
	}
	return out, nil
}
func (this dirBackend) Stat(path string) (os.FileInfo, error)  { return os.Stat(this.p(path)) }
func (this dirBackend) Cat(path string) (io.ReadCloser, error) { return os.Open(this.p(path)) }
func (this dirBackend) Mkdir(path string) error                { return os.Mkdir(this.p(path), 0755) }
func (this dirBackend) Rm(path string) error                   { return os.RemoveAll(this.p(path)) }
func (this dirBackend) Mv(from, to string) error               { return os.Rename(this.p(from), this.p(to)) }
func (this dirBackend) Touch(path string) error                { return os.WriteFile(this.p(path), nil, 0644) }
func (this dirBackend) Save(path string, r io.Reader) error {
	b, _ := io.ReadAll(r)
	return os.WriteFile(this.p(path), b, 0644)
}

// fakeLLM replays a script of tool calls and records what it was sent
type fakeLLM struct {
	script   []Message
	requests [][]Message
}

func (this *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []Message `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	this.requests = append(this.requests, body.Messages)
	msg := Message{Role: "assistant", Content: "all done"}
	if i := len(this.requests) - 1; i < len(this.script) {
		msg = this.script[i]
	}
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}}})
}

func call(id, name, args string) Message {
	tc := ToolCall{ID: id, Type: "function"}
	tc.Function.Name, tc.Function.Arguments = name, args
	return Message{Role: "assistant", ToolCalls: []ToolCall{tc}}
}

func TestAgent(t *testing.T) {
	var err error
	if db, err = sql.Open("sqlite3", filepath.Join(t.TempDir(), "ai.db")); err != nil {
		t.Fatal(err)
	}
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "inbox"), 0755)
	os.MkdirAll(filepath.Join(root, "docs"), 0755)
	os.WriteFile(filepath.Join(root, "inbox", "scan001.pdf"), []byte("same content"), 0644)
	os.WriteFile(filepath.Join(root, "docs", "invoice.pdf"), []byte("same content"), 0644)
	os.WriteFile(filepath.Join(root, "inbox", "notes.txt"), []byte("buy milk"), 0644)

	llm := &fakeLLM{script: []Message{
		call("1", "find_duplicates", `{"path":"/"}`),
		call("2", "read_file", `{"path":"notes.txt"}`),
		call("3", "move", `{"from":"notes.txt","to":"/docs/todo.txt"}`),
		call("4", "move", `{"from":"/docs/","to":"/documents"}`),
		call("5", "delete", `{"path":"scan001.pdf"}`),
		call("6", "remember", `{"fact":"Documents live in /documents"}`),
		call("7", "move", `{"from":"/../../etc/passwd","to":"/x"}`),
	}}
	srv := httptest.NewServer(llm)
	defer srv.Close()

	ctx := &App{Backend: dirBackend{root}, Session: map[string]string{"username": "me"}, Context: context.Background()}
	sess := &Session{ctx: ctx, user: "me", cwd: "/inbox/"}
	res, err := runAgent(context.Background(), LLM{BaseURL: srv.URL}, sess, Request{Message: "tidy up"}, 12)
	if err != nil {
		t.Fatal(err)
	}
	toolOutput := func(id string) string {
		for _, m := range llm.requests[len(llm.requests)-1] {
			if m.Role == "tool" && m.ToolCallID == id {
				return m.Content
			}
		}
		return ""
	}
	if out := toolOutput("1"); !strings.Contains(out, "1 groups") || !strings.Contains(out, "/inbox/scan001.pdf") || !strings.Contains(out, "/docs/invoice.pdf") {
		t.Errorf("duplicates not found: %s", out)
	}
	if out := toolOutput("2"); out != "buy milk" {
		t.Errorf("read_file: %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "documents", "todo.txt")); err != nil {
		t.Errorf("file and directory moves did not happen")
	}
	if len(res.Actions) != 2 || res.Actions[1].From != "/docs/" || res.Actions[1].To != "/documents/" {
		t.Errorf("unexpected actions: %+v", res.Actions)
	}
	if len(res.Pending) != 1 || res.Pending[0].Path != "/inbox/scan001.pdf" {
		t.Errorf("delete should be pending: %+v", res.Pending)
	}
	if _, err := os.Stat(filepath.Join(root, "inbox", "scan001.pdf")); err != nil {
		t.Errorf("file must not be deleted without confirmation")
	}
	if out := toolOutput("7"); !strings.HasPrefix(out, "error") {
		t.Errorf("escaping the root should fail, got %q", out)
	}
	if res.Reply != "all done" {
		t.Errorf("reply: %q", res.Reply)
	}

	// memory and journal are injected in the next conversation
	sess = &Session{ctx: ctx, user: "me", cwd: "/"}
	llm.script, llm.requests = nil, nil
	if _, err := runAgent(context.Background(), LLM{BaseURL: srv.URL}, sess, Request{Message: "hi"}, 12); err != nil {
		t.Fatal(err)
	}
	system := llm.requests[0][0].Content
	if !strings.Contains(system, "Documents live in /documents") || !strings.Contains(system, "moved /docs/ -> /documents/") {
		t.Errorf("memory not in system prompt:\n%s", system)
	}
	if mems := memories("someone-else"); len(mems) != 0 {
		t.Errorf("memory leaked across users")
	}
}

func TestStripThinking(t *testing.T) {
	if got := stripThinking("<think>hmm</think>\nHello"); got != "Hello" {
		t.Errorf("got %q", got)
	}
}

func TestPatchApplies(t *testing.T) {
	files, _, err := gitdiff.Parse(bytes.NewReader(PATCH))
	if err != nil || len(files) != 1 {
		t.Fatalf("cannot parse patch: %v", err)
	}
	orig, err := os.ReadFile("../../../public/assets/pages/ctrl_filespage.js")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := gitdiff.Apply(&out, bytes.NewReader(orig), files[0]); err != nil {
		t.Fatalf("patch does not apply: %v", err)
	}
}
