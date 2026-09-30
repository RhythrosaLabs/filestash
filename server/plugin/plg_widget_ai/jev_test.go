package plg_widget_ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/mickael-kerjean/filestash/server/common"
)

// fakeJev follows the schema of https://api.typesafe.ai/openapi.json
func fakeJev(t *testing.T, calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer jev-key" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			Model     string                    `json:"model"`
			State     map[string]any            `json:"state"`
			Questions map[string]map[string]any `json:"questions"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "jev-latest" {
			t.Errorf("unexpected model %q", req.Model)
		}
		answers := map[string]any{}
		for name, q := range req.Questions {
			switch q["type"] {
			case "choice":
				name, _ := req.State["file_name"].(string)
				excerpt, _ := req.State["content_excerpt"].(string)
				choice, conf := "Other", 0.4
				if strings.HasSuffix(name, ".jpg") {
					choice, conf = "Photos", 0.97
				} else if strings.Contains(excerpt, "Total due") {
					choice, conf = "Invoices", 0.91
				}
				answers["category"] = map[string]any{"type": "choice", "choice": choice, "confidence": conf, "probabilities": map[string]float64{choice: conf}}
			case "noul":
				answers[name] = map[string]any{"type": "noul", "noul": 0.93}
			case "score":
				answers[name] = map[string]any{"type": "score", "score": 1.8, "confidence": 0.8, "legend": map[string]string{"0": "can wait", "1": "this week", "2": "today"}, "probabilities": map[string]float64{"2": 0.8}}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-2026-09-15", "answers": answers, "usage": map[string]int{"input_tokens": 50, "output_tokens": 3}})
	}))
}

func TestJev(t *testing.T) {
	var calls int32
	srv := fakeJev(t, &calls)
	defer srv.Close()
	jevBaseURL = srv.URL
	Config.Get("features.ai.jev_model").Set("jev-latest")

	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "IMG_001.jpg"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "bill.txt"), []byte("ACME corp. Total due: 42$"), 0644)
	os.WriteFile(filepath.Join(root, "misc.bin"), []byte("?"), 0644)
	sess := &Session{ctx: &App{Backend: dirBackend{root}, Session: map[string]string{}, Context: context.Background()}, user: "me", cwd: "/"}
	args := map[string]any{"categories": map[string]any{"Photos": "pictures", "Invoices": "bills", "Other": nil}}

	Config.Get("features.ai.jev_api_key").Set("")
	if _, err := classifyFilesTool(sess, args); err == nil {
		t.Fatal("expected an error when jev isn't configured")
	}
	Config.Get("features.ai.jev_api_key").Set("jev-key")
	out, err := classifyFilesTool(sess, args)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"IMG_001.jpg → Photos (97%)", "bill.txt → Invoices (91%)", "misc.bin → Other (40%)  (uncertain"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	notes := triageInbox(context.Background(), []InboxItem{{Kind: "mention", From: "@bob", Text: "can you send the invoice today?"}, {Kind: "like", From: "@amy"}})
	if notes[0] != " [needs a reply, urgency: today]" || notes[1] != "" {
		t.Errorf("unexpected triage %q", notes)
	}
	Config.Get("features.ai.jev_api_key").Set("wrong")
	if out, _ := classifyFilesTool(sess, args); !strings.Contains(out, "error jev returned 401") {
		t.Errorf("auth errors must surface: %s", out)
	}
	Config.Get("features.ai.jev_api_key").Set("")
}
