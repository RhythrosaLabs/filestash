package plg_widget_ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Jev by TypeSafe AI is a "System One" model: instead of generating text it
// answers typed questions (choice, score, yes/no) about a piece of state in
// one fast pass with calibrated confidence. We use it where a chat model would
// be slow and expensive: sorting many files and triaging notifications.
// API: POST https://api.typesafe.ai/v1/systemone

var jevBaseURL = "https://api.typesafe.ai"

type JevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Noul          float64            `json:"noul"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]any     `json:"legend"`
}

func jevEnabled() bool {
	return strings.TrimSpace(PluginJevKey()) != ""
}

func jevAsk(ctx context.Context, state any, questions map[string]any) (map[string]JevAnswer, error) {
	model := PluginJevModel()
	if model == "" {
		model = "jev-latest"
	}
	body, _ := json.Marshal(map[string]any{"model": model, "state": state, "questions": questions})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(jevBaseURL, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(PluginJevKey()))
	res, err := jevHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jev: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jev returned %d: %s", res.StatusCode, truncate(string(b), 300))
	}
	var out struct {
		Answers map[string]JevAnswer `json:"answers"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("jev: invalid response")
	}
	return out.Answers, nil
}

var jevHTTP = &http.Client{Timeout: 30 * time.Second}

// parallel runs fn over n items with a few workers, keeping the order of results
func parallel(n, workers int, fn func(i int)) {
	var wg sync.WaitGroup
	ch := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
}

func init() {
	toolDefs = append(toolDefs, tool("classify_files",
		"Sort the files of a folder into categories, fast. Returns the best category of each file with a confidence score. Use it before moving many files, then move them with the move tool",
		[]string{"categories"}, map[string]any{
			"path": param("path", "string", "Folder to classify. Default: current directory"),
			"categories": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
				"description": `Category names with a description of what belongs there, eg: {"Invoices": "bills and receipts", "Photos": "pictures", "Other": "anything else"}`},
		}))
	extraTools["classify_files"] = classifyFilesTool
}

func classifyFilesTool(sess *Session, args map[string]any) (string, error) {
	if !jevEnabled() {
		return "", fmt.Errorf("Jev isn't configured (admin > features > ai > jev_api_key): classify the files yourself from list_dir and read_file")
	}
	categories := map[string]any{}
	if c, ok := args["categories"].(map[string]any); ok {
		for k, v := range c {
			if s, ok := v.(string); ok {
				categories[k] = s
			} else {
				categories[k] = nil
			}
		}
	}
	if len(categories) < 2 {
		return "", fmt.Errorf("give at least 2 categories")
	}
	view, full, err := sess.resolve(argStr(args, "path"), true)
	if err != nil {
		return "", err
	}
	if err := sess.authorise("ls", full); err != nil {
		return "", err
	}
	entries, err := sess.ctx.Backend.Ls(full)
	if err != nil {
		return "", err
	}
	type item struct {
		name, category, err string
		confidence          float64
	}
	items := []*item{}
	for _, e := range entries {
		if !e.IsDir() && len(items) < 300 {
			items = append(items, &item{name: e.Name()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	ctx, cancel := context.WithTimeout(sess.context(), 2*time.Minute)
	defer cancel()
	parallel(len(items), 8, func(i int) {
		it := items[i]
		state := map[string]any{"file_name": it.name, "folder": view}
		if isText(it.name) || strings.HasSuffix(strings.ToLower(it.name), ".eml") || strings.HasSuffix(strings.ToLower(it.name), ".ics") {
			if txt, err := sess.readFile(view + it.name); err == nil {
				state["content_excerpt"] = truncate(txt, 2000)
			}
		}
		answers, err := jevAsk(ctx, state, map[string]any{
			"category": map[string]any{"type": "choice", "instructions": "Which category does this file belong to?", "criteria": categories},
		})
		if err != nil {
			it.err = err.Error()
			return
		}
		it.category, it.confidence = answers["category"].Choice, answers["category"].Confidence
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%d files in %s classified by Jev:\n", len(items), view)
	for _, it := range items {
		if it.err != "" {
			fmt.Fprintf(&b, "- %s: error %s\n", it.name, it.err)
			continue
		}
		flag := ""
		if it.confidence < 0.6 {
			flag = "  (uncertain, check with the user)"
		}
		fmt.Fprintf(&b, "- %s → %s (%.0f%%)%s\n", it.name, it.category, it.confidence*100, flag)
	}
	return b.String(), nil
}

// triageInbox annotates notifications with Jev: does it need a reply and how urgent is it
func triageInbox(ctx context.Context, items []InboxItem) []string {
	notes := make([]string, len(items))
	if !jevEnabled() {
		return notes
	}
	parallel(len(items), 8, func(i int) {
		it := items[i]
		if it.Text == "" {
			return
		}
		answers, err := jevAsk(ctx, map[string]string{"kind": it.Kind, "from": it.From, "text": it.Text}, map[string]any{
			"needs_reply": map[string]any{"type": "noul", "instructions": "Does this message deserve a reply from the account owner?"},
			"urgency":     map[string]any{"type": "score", "instructions": "How urgent is it to answer?", "criteria": []string{"can wait", "this week", "today"}},
		})
		if err != nil {
			return
		}
		legend := []string{"can wait", "this week", "today"}
		level := int(answers["urgency"].Score + 0.5)
		if level < 0 || level > 2 {
			level = 0
		}
		reply := "no reply needed"
		if answers["needs_reply"].Noul >= 0.5 {
			reply = "needs a reply"
		}
		notes[i] = fmt.Sprintf(" [%s, urgency: %s]", reply, legend[level])
	})
	return notes
}
