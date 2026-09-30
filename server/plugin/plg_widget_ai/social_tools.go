package plg_widget_ai

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func init() {
	toolDefs = append(toolDefs,
		tool("social_accounts", "List the connected social media accounts", []string{}, map[string]any{}),
		tool("create_post", "Prepare a social media post. It is saved as a draft that the user approves with a click, then it is published right away or at the scheduled time", []string{"account", "text"}, map[string]any{
			"account": param("account", "string", "Account id, name or provider"),
			"text":    param("text", "string", "Text of the post"),
			"media":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Paths of images or videos to attach"},
			"when":    param("when", "string", "Optional publication date in local time, eg: 2026-10-02 18:30. Empty to publish when approved"),
		}),
		tool("list_posts", "List drafts, scheduled, published and failed posts", []string{}, map[string]any{}),
		tool("cancel_post", "Cancel a draft or scheduled post", []string{"id"}, map[string]any{
			"id": param("id", "integer", "Post id"),
		}),
		tool("social_inbox", "Get recent notifications, replies, comments and messages from social accounts", []string{}, map[string]any{
			"account": param("account", "string", "Account id, name or provider. Empty for all accounts"),
			"limit":   param("limit", "integer", "Items per account. Default: 20"),
		}),
		tool("create_routine", "Create a recurring automation: on schedule, pick the next file of a folder that hasn't been posted yet, write a post about it following the instructions and publish it", []string{"account", "folder", "schedule"}, map[string]any{
			"account":      param("account", "string", "Account id, name or provider"),
			"folder":       param("folder", "string", "Folder to pick files from"),
			"instructions": param("instructions", "string", "How to write the posts: tone, hashtags, language, ..."),
			"schedule":     param("schedule", "string", "Cron expression in local time (minute hour day month weekday), eg: '0 18 * * 1,4' for monday and thursday at 18:00, or @daily, @weekly, @weekdays"),
			"review":       param("review", "boolean", "If true, posts are saved as drafts for the user to approve instead of being published automatically"),
		}),
		tool("list_routines", "List the recurring post automations", []string{}, map[string]any{}),
		tool("delete_routine", "Delete a recurring post automation", []string{"id"}, map[string]any{
			"id": param("id", "integer", "Routine id"),
		}),
		tool("run_routine", "Run a routine right now instead of waiting for its schedule", []string{"id"}, map[string]any{
			"id": param("id", "integer", "Routine id"),
		}),
	)
}

type socialTool func(sess *Session, args map[string]any) (string, error)

func argStr(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return strings.TrimSpace(v)
}

func argInt(args map[string]any, k string, def int64) int64 {
	switch v := args[k].(type) {
	case float64:
		return int64(v)
	case string:
		var n int64
		if _, err := fmt.Sscan(strings.TrimPrefix(v, "#"), &n); err == nil {
			return n
		}
	}
	return def
}

func argBool(args map[string]any, k string) bool {
	switch v := args[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "yes"
	}
	return false
}

func argList(args map[string]any, k string) []string {
	out := []string{}
	switch v := args[k].(type) {
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func parseWhen(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "now") {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot understand the date %q, use YYYY-MM-DD HH:MM", s)
}

func formatTime(unix int64) string {
	if unix == 0 {
		return "when approved"
	}
	return time.Unix(unix, 0).Format("Mon 2006-01-02 15:04")
}

var extraTools = map[string]socialTool{
	"social_accounts": func(sess *Session, args map[string]any) (string, error) {
		accounts := listAccounts(sess.user)
		if len(accounts) == 0 {
			return "no social account connected yet, the user can connect one from the 🔗 button of the assistant", nil
		}
		var b strings.Builder
		for _, a := range accounts {
			fmt.Fprintf(&b, "#%d %s %s (max %d characters)\n", a.ID, a.Provider, a.Name, providers[a.Provider].MaxLength())
		}
		return b.String(), nil
	},

	"create_post": func(sess *Session, args map[string]any) (string, error) {
		account, err := findAccount(sess.user, argStr(args, "account"))
		if err != nil {
			return "", err
		}
		provider := providers[account.Provider]
		text := argStr(args, "text")
		if err := checkLength(provider, text); err != nil {
			return "", err
		}
		when, err := parseWhen(argStr(args, "when"))
		if err != nil {
			return "", err
		}
		if !when.IsZero() && when.Before(time.Now().Add(-time.Minute)) {
			return "", fmt.Errorf("%s is in the past", when.Format("2006-01-02 15:04"))
		}
		post := Post{user: sess.user, AccountID: account.ID, Account: account.Name, Text: text, Status: StatusDraft, Media: []string{}}
		if !when.IsZero() {
			post.ScheduledAt = when.Unix()
		}
		for _, p := range argList(args, "media") {
			if !isMedia(p) {
				cleanupMedia(post.Media)
				return "", fmt.Errorf("%s is not an image or a video", p)
			}
			staged, err := sess.stageFile(p)
			if err != nil {
				cleanupMedia(post.Media)
				return "", err
			}
			post.Media = append(post.Media, staged)
			post.Source = p
		}
		if account.Provider == "instagram" && len(post.Media) == 0 {
			return "", fmt.Errorf("Instagram posts need an image or a video")
		} else if account.Provider == "youtube" && (len(post.Media) != 1 || !isVideo(post.Media[0])) {
			cleanupMedia(post.Media)
			return "", fmt.Errorf("YouTube posts need exactly one video (mp4, mov, m4v or webm). The first line of the text is the title, the rest the description")
		}
		if post.ID, err = createPost(post); err != nil {
			cleanupMedia(post.Media)
			return "", err
		}
		sess.Drafts = append(sess.Drafts, post)
		return fmt.Sprintf("draft #%d created for %s, to be published %s once the user clicks approve", post.ID, account.Name, formatTime(post.ScheduledAt)), nil
	},

	"list_posts": func(sess *Session, args map[string]any) (string, error) {
		posts := listPosts(sess.user, 30)
		if len(posts) == 0 {
			return "no posts", nil
		}
		var b strings.Builder
		for _, p := range posts {
			fmt.Fprintf(&b, "#%d [%s] %s, %s: %s", p.ID, p.Status, p.Account, formatTime(p.ScheduledAt), truncate(p.Text, 100))
			if p.URL != "" {
				fmt.Fprintf(&b, " → %s", p.URL)
			}
			if p.Error != "" {
				fmt.Fprintf(&b, " (error: %s)", p.Error)
			}
			b.WriteString("\n")
		}
		return b.String(), nil
	},

	"cancel_post": func(sess *Session, args map[string]any) (string, error) {
		id := argInt(args, "id", -1)
		if err := cancelPost(sess.user, id); err != nil {
			return "", err
		}
		return fmt.Sprintf("post #%d cancelled", id), nil
	},

	"social_inbox": func(sess *Session, args map[string]any) (string, error) {
		limit := int(argInt(args, "limit", 20))
		if limit <= 0 || limit > 50 {
			limit = 20
		}
		accounts := []Account{}
		if ref := argStr(args, "account"); ref != "" {
			a, err := findAccount(sess.user, ref)
			if err != nil {
				return "", err
			}
			accounts = append(accounts, a)
		} else {
			for _, a := range listAccounts(sess.user) {
				if full, err := getAccount(sess.user, a.ID); err == nil {
					accounts = append(accounts, full)
				}
			}
		}
		if len(accounts) == 0 {
			return "no social account connected", nil
		}
		ctx, cancel := context.WithTimeout(sess.context(), 60*time.Second)
		defer cancel()
		var b strings.Builder
		for _, a := range accounts {
			items, err := providers[a.Provider].Inbox(ctx, a.creds, limit)
			fmt.Fprintf(&b, "## %s %s\n", a.Provider, a.Name)
			if err != nil {
				fmt.Fprintf(&b, "error: %s\n", err.Error())
				continue
			} else if len(items) == 0 {
				b.WriteString("nothing new\n")
			}
			notes := triageInbox(ctx, items)
			for i, it := range items {
				fmt.Fprintf(&b, "- %s %s from %s: %s %s%s\n", it.Time.Local().Format("01-02 15:04"), it.Kind, it.From, truncate(strings.ReplaceAll(it.Text, "\n", " "), 200), it.URL, notes[i])
			}
		}
		return b.String(), nil
	},

	"create_routine": func(sess *Session, args map[string]any) (string, error) {
		account, err := findAccount(sess.user, argStr(args, "account"))
		if err != nil {
			return "", err
		}
		cron, err := normalizeCron(argStr(args, "schedule"))
		if err != nil {
			return "", err
		}
		folder, full, err := sess.resolve(argStr(args, "folder"), true)
		if err != nil {
			return "", err
		}
		if _, err := sess.ctx.Backend.Ls(full); err != nil {
			return "", fmt.Errorf("cannot open %s: %w", folder, err)
		}
		if sess.token == "" {
			return "", fmt.Errorf("routines can't be created from this session")
		}
		r := Routine{user: sess.user, AccountID: account.ID, Folder: folder, Instructions: argStr(args, "instructions"), Cron: cron, Review: argBool(args, "review"), token: sess.token}
		id, err := createRoutine(r)
		if err != nil {
			return "", err
		}
		mode := "published automatically"
		if r.Review {
			mode = "saved as drafts for approval"
		}
		return fmt.Sprintf("routine #%d created: files from %s posted to %s on schedule '%s', %s", id, folder, account.Name, cron, mode), nil
	},

	"list_routines": func(sess *Session, args map[string]any) (string, error) {
		routines := listRoutines(sess.user)
		if len(routines) == 0 {
			return "no routine", nil
		}
		var b strings.Builder
		for _, r := range routines {
			last := "never"
			if r.LastRun > 0 {
				last = formatTime(r.LastRun)
			}
			fmt.Fprintf(&b, "#%d %s → %s, schedule '%s', review=%v, %d files posted, last run %s. Instructions: %s\n",
				r.ID, r.Folder, r.Account, r.Cron, r.Review, len(r.used), last, r.Instructions)
		}
		return b.String(), nil
	},

	"delete_routine": func(sess *Session, args map[string]any) (string, error) {
		id := argInt(args, "id", -1)
		if err := deleteRoutine(sess.user, id); err != nil {
			return "", err
		}
		return fmt.Sprintf("routine #%d deleted", id), nil
	},

	"run_routine": func(sess *Session, args map[string]any) (string, error) {
		id := argInt(args, "id", -1)
		for _, r := range listRoutines(sess.user) {
			if r.ID == id {
				return runRoutine(sess.context(), r)
			}
		}
		return "", fmt.Errorf("routine #%d not found", id)
	},
}

func (this *Session) context() context.Context {
	if this.ctx != nil && this.ctx.Context != nil {
		return this.ctx.Context
	}
	return context.Background()
}
