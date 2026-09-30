package plg_widget_ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/mickael-kerjean/filestash/server/common"
)

func startSocial() {
	if err := initSocialSchema(db); err != nil {
		Log.Error("plg_widget_ai::social err=cannot_init msg=%s", err.Error())
		return
	}
	// a post interrupted by a restart is marked failed rather than risking a double post
	db.Exec(`UPDATE social_posts SET status = ?, error = 'interrupted by a restart, retry manually' WHERE status = ?`, StatusFailed, StatusPosting)
	go func() {
		for {
			time.Sleep(time.Until(time.Now().Truncate(time.Minute).Add(time.Minute)))
			if PluginEnable() {
				socialTick(time.Now())
			}
		}
	}()
}

func socialTick(now time.Time) {
	for _, p := range duePosts(now) {
		go publish(context.Background(), p)
	}
	for _, r := range listRoutines("") {
		if r.LastRun >= now.Truncate(time.Minute).Unix() || !cronMatch(r.Cron, now) {
			continue
		}
		go func(r Routine) {
			if msg, err := runRoutine(context.Background(), r); err != nil {
				Log.Warning("plg_widget_ai::routine id=%d err=%s", r.ID, err.Error())
			} else {
				Log.Info("plg_widget_ai::routine id=%d %s", r.ID, msg)
			}
		}(r)
	}
}

func publicMediaURL(name string) string {
	host := strings.TrimSuffix(Config.Get("general.host").String(), "/")
	if host == "" {
		return ""
	}
	if !strings.HasPrefix(host, "http") {
		host = "https://" + host
	}
	return host + WithBase("/api/plg_widget_ai/social/media/"+name)
}

// publish sends a scheduled post, only once
func publish(ctx context.Context, p Post) (string, error) {
	if !claimPost(p.ID) {
		return "", NewError("post is not in the queue", 409)
	}
	url, err := doPublish(ctx, p)
	finishPost(p.ID, url, err)
	if err == nil {
		cleanupMedia(p.Media)
		journal(p.user, fmt.Sprintf("published post #%d to %s: %s", p.ID, p.Account, truncate(p.Text, 80)))
	}
	return url, err
}

func doPublish(ctx context.Context, p Post) (string, error) {
	account, err := getAccount(p.user, p.AccountID)
	if err != nil {
		return "", NewError("the account of this post was removed", 404)
	}
	provider, ok := providers[account.Provider]
	if !ok {
		return "", ErrNotImplemented
	}
	media := []Media{}
	for i, name := range p.Media {
		data, err := readMedia(name)
		if err != nil {
			return "", NewError("media of the post are missing", 404)
		}
		if account.Provider == "instagram" && isImage(name) && mimeOf(name) != "image/jpeg" {
			if data, err = toJPEG(data, 8<<20); err != nil { // instagram only accepts jpeg
				return "", err
			}
			if converted, err := stageMedia("x.jpg", data); err == nil {
				cleanupMedia([]string{name})
				name, p.Media[i] = converted, converted
				m, _ := json.Marshal(p.Media)
				db.Exec(`UPDATE social_posts SET media = ? WHERE id = ?`, string(m), p.ID)
			}
		}
		media = append(media, Media{Name: name, Data: data, PublicURL: publicMediaURL(name)})
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return provider.Post(ctx, account.creds, p.Text, media)
}

// ---------------------------------------------------------------- routines

// openRoutineApp recreates the storage session a routine was created with
var openRoutineApp = func(token string) (*App, error) {
	session := map[string]string{}
	if err := decrypt(token, &session); err != nil {
		return nil, err
	}
	app := &App{Context: context.Background(), Session: session}
	backend, err := NewBackend(app, session)
	if err != nil {
		return nil, err
	}
	app.Backend = backend
	return app, nil
}

func isMedia(name string) bool {
	return isImage(name) || strings.HasSuffix(strings.ToLower(name), ".mp4") || strings.HasSuffix(strings.ToLower(name), ".mov")
}

func isText(name string) bool {
	for _, ext := range []string{".txt", ".md", ".markdown", ".html", ".csv", ".json", ".org", ".rst"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return false
}

func runRoutine(ctx context.Context, r Routine) (string, error) {
	markRoutineRun(r, "") // mark first so a failing routine doesn't retry in a loop
	app, err := openRoutineApp(r.token)
	if err != nil {
		return "", fmt.Errorf("cannot open the storage: %w", err)
	}
	account, err := getAccount(r.user, r.AccountID)
	if err != nil {
		return "", NewError("the account of this routine was removed", 404)
	}
	provider := providers[account.Provider]
	sess := &Session{ctx: app, user: r.user, cwd: "/"}
	folder, full, err := sess.resolve(r.Folder, true)
	if err != nil {
		return "", err
	}
	files, err := app.Backend.Ls(full)
	if err != nil {
		return "", fmt.Errorf("cannot list %s: %w", folder, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	used := map[string]bool{}
	for _, u := range r.used {
		used[u] = true
	}
	pick := ""
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || used[folder+name] || strings.HasPrefix(name, ".") {
			continue
		}
		if account.Provider == "instagram" && !isMedia(name) {
			continue
		}
		if !isMedia(name) && !isText(name) {
			continue
		}
		pick = name
		break
	}
	if pick == "" {
		return "nothing new to post in " + folder, nil
	}
	data, err := sess.readBytes(folder+pick, maxMediaSize)
	if err != nil {
		return "", err
	}
	llm := LLM{BaseURL: PluginBaseURL(), Model: PluginModel(), APIKey: PluginAPIKey()}
	text, err := writeCaption(ctx, llm, provider, r.user, r.Instructions, pick, data)
	if err != nil {
		return "", err
	}
	post := Post{user: r.user, AccountID: account.ID, Text: text, Status: StatusDraft, Source: folder + pick}
	if isMedia(pick) && (account.Provider != "bluesky" || isImage(pick)) {
		staged, err := stageMedia(pick, data)
		if err != nil {
			return "", err
		}
		post.Media = []string{staged}
	}
	if !r.Review {
		post.Status, post.ScheduledAt = StatusScheduled, time.Now().Unix()
	}
	id, err := createPost(post)
	if err != nil {
		return "", err
	}
	post.ID, post.Account = id, account.Name
	markRoutineRun(r, folder+pick)
	if r.Review {
		return fmt.Sprintf("draft #%d about %s is waiting for approval", id, pick), nil
	}
	url, err := publish(ctx, post)
	if err != nil {
		return "", fmt.Errorf("post #%d about %s failed: %w", id, pick, err)
	}
	return fmt.Sprintf("posted %s to %s: %s", pick, account.Name, url), nil
}

// writeCaption asks the model to write a post about a file, looking at the
// image when the model supports vision and falling back to the name otherwise
func writeCaption(ctx context.Context, llm LLM, provider Provider, user, instructions, name string, data []byte) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Write a %s post, at most %d characters, about the file %q.\n", provider.Title(), provider.MaxLength()-20, name)
	if instructions != "" {
		fmt.Fprintf(&b, "Instructions: %s\n", instructions)
	}
	if mems := memories(user); len(mems) > 0 {
		b.WriteString("What you know about the user (use it for tone and context):\n")
		for _, m := range mems {
			fmt.Fprintf(&b, "- %s\n", m.Content)
		}
	}
	var image []byte
	if isText(name) {
		fmt.Fprintf(&b, "Content of the file:\n%s\n", truncate(string(data), 8000))
	} else if isImage(name) {
		if jpg, err := toJPEG(data, 1<<20); err == nil {
			image = jpg
			b.WriteString("The image is attached.\n")
		}
	}
	b.WriteString("Reply with the post text only: no quotes, no preamble, no explanation.")
	system := "You are a social media copywriter writing posts on behalf of the user. You write natural, engaging posts in the user's voice."
	text, err := llm.Complete(ctx, system, b.String(), image)
	if err != nil && image != nil {
		prompt := strings.Replace(b.String(), "The image is attached.\n", "", 1)
		text, err = llm.Complete(ctx, system, prompt, nil)
	}
	if err != nil {
		return "", err
	}
	text = strings.Trim(strings.TrimSpace(text), "\"")
	if text == "" {
		return "", NewError("the model returned an empty post", 500)
	}
	if utf8.RuneCountInString(text) > provider.MaxLength() {
		r := []rune(text)[:provider.MaxLength()-1]
		if i := strings.LastIndexAny(string(r), " \n"); i > len(string(r))/2 {
			r = []rune(string(r)[:i])
		}
		text = strings.TrimSpace(string(r)) + "…"
	}
	return text, nil
}

// Complete is a single turn completion that can include an image (OpenAI vision format)
func (this LLM) Complete(ctx context.Context, system, prompt string, image []byte) (string, error) {
	var content any = prompt
	if image != nil {
		content = []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(image)}},
		}
	}
	body, _ := json.Marshal(map[string]any{
		"model":       this.Model,
		"messages":    []any{map[string]any{"role": "system", "content": system}, map[string]any{"role": "user", "content": content}},
		"temperature": 0.7,
		"stream":      false,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(this.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if this.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+this.APIKey)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("model returned %d: %s", res.StatusCode, truncate(string(b), 300))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("invalid model response")
	}
	return stripThinking(out.Choices[0].Message.Content), nil
}

// ---------------------------------------------------------------- cron

var cronAliases = map[string]string{
	"@hourly": "0 * * * *", "@daily": "0 9 * * *", "@weekly": "0 9 * * 1",
	"@monthly": "0 9 1 * *", "@weekdays": "0 9 * * 1-5",
}

func normalizeCron(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	if v, ok := cronAliases[expr]; ok {
		expr = v
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return "", NewError("schedule must be a cron expression like '0 9 * * 1' (minute hour day month weekday) or @hourly, @daily, @weekly, @monthly, @weekdays", 400)
	}
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, f := range fields {
		if _, err := cronField(f, limits[i][0], limits[i][1]); err != nil {
			return "", NewError("invalid cron field '"+f+"'", 400)
		}
	}
	return expr, nil
}

func cronField(field string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.Index(part, "/"); i != -1 {
			s, err := strconv.Atoi(part[i+1:])
			if err != nil || s <= 0 {
				return nil, ErrNotValid
			}
			step, part = s, part[:i]
		}
		lo, hi := min, max
		if part != "*" {
			bounds := strings.SplitN(part, "-", 2)
			var err error
			if lo, err = strconv.Atoi(bounds[0]); err != nil {
				return nil, ErrNotValid
			}
			hi = lo
			if len(bounds) == 2 {
				if hi, err = strconv.Atoi(bounds[1]); err != nil {
					return nil, ErrNotValid
				}
			} else if step > 1 {
				hi = max
			}
		}
		if lo < min || hi > max || lo > hi {
			return nil, ErrNotValid
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}

func cronMatch(expr string, t time.Time) bool {
	expr, err := normalizeCron(expr)
	if err != nil {
		return false
	}
	f := strings.Fields(expr)
	check := func(field string, v, min, max int) bool {
		m, err := cronField(field, min, max)
		return err == nil && m[v]
	}
	weekday := int(t.Weekday())
	return check(f[0], t.Minute(), 0, 59) && check(f[1], t.Hour(), 0, 23) &&
		check(f[2], t.Day(), 1, 31) && check(f[3], int(t.Month()), 1, 12) &&
		(check(f[4], weekday, 0, 7) || (weekday == 0 && check(f[4], 7, 0, 7)))
}
