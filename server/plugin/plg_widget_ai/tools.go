package plg_widget_ai

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	. "github.com/mickael-kerjean/filestash/server/common"
	"github.com/mickael-kerjean/filestash/server/pkg/permissions"
)

const (
	maxReadBytes    = 32 * 1024
	maxWalkDirs     = 2000
	maxWalkFiles    = 20000
	maxSearchResult = 100
	maxHashBytes    = int64(2 << 30)
)

// Session is the state of one agent run: the user's Filestash session plus
// what happened during the run so the UI can show it.
type Session struct {
	ctx     *App
	user    string
	cwd     string
	token   string   // encrypted storage session, lets routines access files later
	Actions []Action `json:"actions"`
	Pending []Action `json:"pending"`
	Drafts  []Post   `json:"drafts"`
}

type Action struct {
	Op   string `json:"op"`
	Path string `json:"path,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

func param(name, typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func tool(name, desc string, required []string, props map[string]any) ToolDef {
	return ToolDef{Type: "function", Function: ToolFunction{
		Name:        name,
		Description: desc,
		Parameters:  map[string]any{"type": "object", "properties": props, "required": required},
	}}
}

var toolDefs = []ToolDef{
	tool("list_dir", "List the content of a directory", []string{}, map[string]any{
		"path": param("path", "string", "Directory path, absolute or relative to the current directory. Default: current directory"),
	}),
	tool("read_file", "Read the content of a text file (first 32KB)", []string{"path"}, map[string]any{
		"path": param("path", "string", "File path"),
	}),
	tool("search", "Recursively search files and folders whose name contains the query (case insensitive)", []string{"query"}, map[string]any{
		"query":     param("query", "string", "Part of the name to look for, eg: 'invoice' or '.pdf'"),
		"path":      param("path", "string", "Where to start searching. Default: current directory"),
		"max_depth": param("max_depth", "integer", "How deep to go. Default: 6"),
	}),
	tool("find_duplicates", "Recursively find files with identical content (same size and sha256)", []string{}, map[string]any{
		"path": param("path", "string", "Where to start. Default: current directory"),
	}),
	tool("make_dir", "Create a directory", []string{"path"}, map[string]any{
		"path": param("path", "string", "Directory path to create"),
	}),
	tool("move", "Move or rename a file or directory", []string{"from", "to"}, map[string]any{
		"from": param("from", "string", "Current path"),
		"to":   param("to", "string", "New path, including the new name"),
	}),
	tool("delete", "Ask the user to confirm the deletion of a file or directory. Nothing is deleted until they click confirm", []string{"path"}, map[string]any{
		"path": param("path", "string", "Path to delete"),
	}),
	tool("remember", "Save a durable fact about the user to long term memory: preferences, naming conventions, where things belong, projects, people", []string{"fact"}, map[string]any{
		"fact": param("fact", "string", "Short self contained fact, eg: 'Invoices go in /Finance/Invoices/YYYY'"),
	}),
	tool("forget", "Remove a fact from long term memory", []string{"id"}, map[string]any{
		"id": param("id", "integer", "Id of the memory"),
	}),
}

func (this *Session) call(name string, rawArgs string) (out string) {
	args := map[string]any{}
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return "error: arguments are not valid json"
		}
	}
	str := func(k string) string {
		v, _ := args[k].(string)
		return strings.TrimSpace(v)
	}
	num := func(k string, def int) int {
		switch v := args[k].(type) {
		case float64:
			return int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		return def
	}
	var err error
	switch name {
	case "list_dir":
		out, err = this.listDir(str("path"))
	case "read_file":
		out, err = this.readFile(str("path"))
	case "search":
		out, err = this.search(str("query"), str("path"), num("max_depth", 6))
	case "find_duplicates":
		out, err = this.findDuplicates(str("path"))
	case "make_dir":
		out, err = this.makeDir(str("path"))
	case "move":
		out, err = this.move(str("from"), str("to"))
	case "delete":
		out, err = this.delete(str("path"))
	case "remember":
		var id int64
		if id, err = remember(this.user, str("fact")); err == nil {
			out = fmt.Sprintf("saved as memory #%d", id)
		}
	case "forget":
		if err = forget(this.user, int64(num("id", -1))); err == nil {
			out = "forgotten"
		}
	default:
		if fn, ok := extraTools[name]; ok {
			out, err = fn(this, args)
			break
		}
		return "error: unknown tool " + name
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}

// resolve turns a path given by the model into a user facing path (what the
// user sees in the UI) and the path to use against the storage backend
func (this *Session) resolve(p string, dir bool) (string, string, error) {
	if p == "" || p == "." {
		p = this.cwd
	} else if !strings.HasPrefix(p, "/") {
		p = strings.TrimSuffix(this.cwd, "/") + "/" + p
	}
	isDir := dir || strings.HasSuffix(p, "/")
	p = filepath.ToSlash(filepath.Clean(p))
	if isDir && p != "/" {
		p += "/"
	}
	full, err := PathBuilder(this.ctx, p)
	return p, full, err
}

func (this *Session) authorise(op string, paths ...string) error {
	for _, auth := range Hooks.Get.AuthorisationMiddleware() {
		var err error
		switch op {
		case "ls":
			err = auth.Ls(this.ctx, paths[0])
		case "cat":
			err = auth.Cat(this.ctx, paths[0])
		case "mkdir":
			err = auth.Mkdir(this.ctx, paths[0])
		case "mv":
			err = auth.Mv(this.ctx, paths[0], paths[1])
		case "rm":
			err = auth.Rm(this.ctx, paths[0])
		}
		if err != nil {
			return ErrNotAuthorized
		}
	}
	return nil
}

func (this *Session) listDir(p string) (string, error) {
	if !permissions.CanRead(this.ctx) {
		return "", ErrPermissionDenied
	}
	view, full, err := this.resolve(p, true)
	if err != nil {
		return "", err
	}
	if err = this.authorise("ls", full); err != nil {
		return "", err
	}
	files, err := this.ctx.Backend.Ls(full)
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d entries)\n", view, len(files))
	for i, f := range files {
		if i >= 300 {
			fmt.Fprintf(&b, "... %d more\n", len(files)-i)
			break
		}
		if f.IsDir() {
			fmt.Fprintf(&b, "[DIR]  %s/\n", f.Name())
		} else {
			fmt.Fprintf(&b, "[FILE] %s  %s  %s\n", f.Name(), humanSize(f.Size()), f.ModTime().Format("2006-01-02"))
		}
	}
	return b.String(), nil
}

func (this *Session) readBytes(p string, limit int64) ([]byte, error) {
	if !permissions.CanRead(this.ctx) {
		return nil, ErrPermissionDenied
	}
	_, full, err := this.resolve(p, false)
	if err != nil {
		return nil, err
	}
	if err = this.authorise("cat", full); err != nil {
		return nil, err
	}
	r, err := this.ctx.Backend.Cat(full)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	} else if int64(len(b)) > limit {
		return nil, NewError(fmt.Sprintf("%s is too large (max %s)", p, humanSize(limit)), 400)
	}
	return b, nil
}

// stageFile copies a file of the storage to the social media staging area, streaming it
func (this *Session) stageFile(p string) (string, error) {
	if !permissions.CanRead(this.ctx) {
		return "", ErrPermissionDenied
	}
	_, full, err := this.resolve(p, false)
	if err != nil {
		return "", err
	}
	if err = this.authorise("cat", full); err != nil {
		return "", err
	}
	r, err := this.ctx.Backend.Cat(full)
	if err != nil {
		return "", err
	}
	defer r.Close()
	limit := int64(maxMediaSize)
	if isVideo(p) {
		limit = maxVideoSize
	}
	return stageReader(filepath.Base(p), r, limit)
}

func (this *Session) readFile(p string) (string, error) {
	if !permissions.CanRead(this.ctx) {
		return "", ErrPermissionDenied
	}
	_, full, err := this.resolve(p, false)
	if err != nil {
		return "", err
	}
	if err = this.authorise("cat", full); err != nil {
		return "", err
	}
	r, err := this.ctx.Backend.Cat(full)
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, maxReadBytes+1))
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) != -1 || !utf8.Valid(b[:max(0, min(len(b), maxReadBytes)-4)]) {
		return fmt.Sprintf("binary file, the content can't be displayed (first %d bytes read)", len(b)), nil
	}
	if len(b) > maxReadBytes {
		return string(b[:maxReadBytes]) + "\n... [truncated]", nil
	}
	return string(b), nil
}

type walkEntry struct {
	view string
	full string
	info os.FileInfo
}

// walk goes through the tree breadth first with hard limits so a huge storage can't hang the request
func (this *Session) walk(p string, maxDepth int, fn func(e walkEntry) bool) (bool, error) {
	if !permissions.CanRead(this.ctx) {
		return false, ErrPermissionDenied
	}
	view, full, err := this.resolve(p, true)
	if err != nil {
		return false, err
	}
	type item struct {
		view, full string
		depth      int
	}
	queue := []item{{view, full, 0}}
	dirs, files := 0, 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if dirs++; dirs > maxWalkDirs {
			return true, nil
		}
		if this.ctx.Context != nil && this.ctx.Context.Err() != nil {
			return true, this.ctx.Context.Err()
		}
		if this.authorise("ls", cur.full) != nil {
			continue
		}
		entries, err := this.ctx.Backend.Ls(cur.full)
		if err != nil {
			if cur.depth == 0 {
				return false, err
			}
			continue
		}
		for _, e := range entries {
			if files++; files > maxWalkFiles {
				return true, nil
			}
			ev, ef := cur.view+e.Name(), cur.full+e.Name()
			if e.IsDir() {
				ev, ef = ev+"/", ef+"/"
				if cur.depth+1 < maxDepth {
					queue = append(queue, item{ev, ef, cur.depth + 1})
				}
			}
			if !fn(walkEntry{ev, ef, e}) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (this *Session) search(query, p string, maxDepth int) (string, error) {
	if query == "" {
		return "", ErrNotValid
	}
	if maxDepth <= 0 || maxDepth > 20 {
		maxDepth = 6
	}
	q := strings.ToLower(query)
	results := []string{}
	partial, err := this.walk(p, maxDepth, func(e walkEntry) bool {
		if strings.Contains(strings.ToLower(e.info.Name()), q) {
			if e.info.IsDir() {
				results = append(results, e.view)
			} else {
				results = append(results, fmt.Sprintf("%s  %s  %s", e.view, humanSize(e.info.Size()), e.info.ModTime().Format("2006-01-02")))
			}
		}
		return len(results) < maxSearchResult
	})
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("%d results\n%s", len(results), strings.Join(results, "\n"))
	if partial {
		out += "\n(search stopped early because of size limits, narrow down the path for more)"
	}
	return out, nil
}

func (this *Session) findDuplicates(p string) (string, error) {
	bySize := map[int64][]walkEntry{}
	partial, err := this.walk(p, 20, func(e walkEntry) bool {
		if !e.info.IsDir() && e.info.Size() > 0 {
			bySize[e.info.Size()] = append(bySize[e.info.Size()], e)
		}
		return true
	})
	if err != nil {
		return "", err
	}
	groups, hashedBytes := findDuplicateGroups(bySize, func(e walkEntry) (string, error) {
		if this.authorise("cat", e.full) != nil {
			return "", ErrNotAuthorized
		}
		r, err := this.ctx.Backend.Cat(e.full)
		if err != nil {
			return "", err
		}
		defer r.Close()
		h := sha256.New()
		if _, err := io.Copy(h, r); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	})
	var b strings.Builder
	var wasted int64
	for _, g := range groups {
		wasted += g[0].info.Size() * int64(len(g)-1)
	}
	fmt.Fprintf(&b, "%d groups of duplicates, %s could be reclaimed\n", len(groups), humanSize(wasted))
	for i, g := range groups {
		if i >= 50 {
			fmt.Fprintf(&b, "... %d more groups\n", len(groups)-i)
			break
		}
		fmt.Fprintf(&b, "- %s each:\n", humanSize(g[0].info.Size()))
		for _, e := range g {
			fmt.Fprintf(&b, "    %s  %s\n", e.view, e.info.ModTime().Format("2006-01-02"))
		}
	}
	if partial || hashedBytes >= maxHashBytes {
		b.WriteString("(scan stopped early because of size limits, narrow down the path for a complete result)\n")
	}
	return b.String(), nil
}

// findDuplicateGroups only hashes files that share their size with another file
func findDuplicateGroups(bySize map[int64][]walkEntry, hash func(walkEntry) (string, error)) ([][]walkEntry, int64) {
	sizes := make([]int64, 0, len(bySize))
	for s, entries := range bySize {
		if len(entries) > 1 {
			sizes = append(sizes, s)
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] })
	groups := [][]walkEntry{}
	var hashed int64
	for _, s := range sizes {
		byHash := map[string][]walkEntry{}
		order := []string{}
		for _, e := range bySize[s] {
			if hashed >= maxHashBytes {
				break
			}
			h, err := hash(e)
			if err != nil {
				continue
			}
			hashed += s
			if _, ok := byHash[h]; !ok {
				order = append(order, h)
			}
			byHash[h] = append(byHash[h], e)
		}
		for _, h := range order {
			if len(byHash[h]) > 1 {
				groups = append(groups, byHash[h])
			}
		}
	}
	return groups, hashed
}

func (this *Session) makeDir(p string) (string, error) {
	if !permissions.CanEdit(this.ctx) {
		return "", ErrPermissionDenied
	}
	view, full, err := this.resolve(p, true)
	if err != nil {
		return "", err
	}
	if err = this.authorise("mkdir", full); err != nil {
		return "", err
	}
	if err = this.ctx.Backend.Mkdir(full); err != nil {
		return "", err
	}
	this.Actions = append(this.Actions, Action{Op: "mkdir", Path: view})
	journal(this.user, "created folder "+view)
	return "created " + view, nil
}

func (this *Session) isDir(full string) bool {
	if strings.HasSuffix(full, "/") {
		return true
	}
	if info, err := this.ctx.Backend.Stat(full); err == nil {
		return info.IsDir()
	}
	return false
}

func (this *Session) move(from, to string) (string, error) {
	if !permissions.CanEdit(this.ctx) {
		return "", ErrPermissionDenied
	}
	if from == "" || to == "" {
		return "", ErrNotValid
	}
	_, fullFrom, err := this.resolve(from, false)
	if err != nil {
		return "", err
	}
	dir := this.isDir(fullFrom)
	viewFrom, fullFrom, err := this.resolve(from, dir)
	if err != nil {
		return "", err
	}
	viewTo, fullTo, err := this.resolve(to, dir)
	if err != nil {
		return "", err
	}
	if viewFrom == "/" || fullFrom == fullTo {
		return "", ErrNotValid
	}
	if _, err := this.ctx.Backend.Stat(fullTo); err == nil {
		return "", NewError("destination already exists", 409)
	}
	if err = this.authorise("mv", fullFrom, fullTo); err != nil {
		return "", err
	}
	if err = this.ctx.Backend.Mv(fullFrom, fullTo); err != nil {
		return "", err
	}
	this.Actions = append(this.Actions, Action{Op: "move", From: viewFrom, To: viewTo})
	journal(this.user, fmt.Sprintf("moved %s -> %s", viewFrom, viewTo))
	return fmt.Sprintf("moved %s to %s", viewFrom, viewTo), nil
}

func (this *Session) delete(p string) (string, error) {
	if !permissions.CanEdit(this.ctx) {
		return "", ErrPermissionDenied
	}
	_, full, err := this.resolve(p, false)
	if err != nil {
		return "", err
	}
	view, _, err := this.resolve(p, this.isDir(full))
	if err != nil {
		return "", err
	}
	if view == "/" {
		return "", ErrNotValid
	}
	this.Pending = append(this.Pending, Action{Op: "delete", Path: view})
	return "the user has been asked to confirm the deletion of " + view + ". It is NOT deleted yet", nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
