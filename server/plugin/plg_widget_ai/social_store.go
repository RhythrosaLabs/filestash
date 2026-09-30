package plg_widget_ai

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "image/gif"
	_ "image/png"

	. "github.com/mickael-kerjean/filestash/server/common"
	"github.com/mickael-kerjean/filestash/server/pkg/env"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// social media: connected accounts, a queue of posts and routines that turn
// files into posts on a schedule. Credentials are encrypted at rest, media are
// copied in a staging area when a post is created so publishing later doesn't
// depend on the user's storage session.

func initSocialSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS social_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user TEXT NOT NULL,
			provider TEXT NOT NULL,
			name TEXT NOT NULL,
			creds TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS social_posts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user TEXT NOT NULL,
			account_id INTEGER NOT NULL,
			text TEXT NOT NULL,
			media TEXT NOT NULL DEFAULT '[]',
			scheduled_at INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			url TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS social_routines (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user TEXT NOT NULL,
			account_id INTEGER NOT NULL,
			folder TEXT NOT NULL,
			instructions TEXT NOT NULL,
			cron TEXT NOT NULL,
			review INTEGER NOT NULL DEFAULT 0,
			token TEXT NOT NULL,
			used TEXT NOT NULL DEFAULT '[]',
			last_run INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);
	`)
	return err
}

const (
	StatusDraft     = "draft"     // waiting for the user to approve
	StatusScheduled = "scheduled" // will be published at scheduled_at
	StatusPosting   = "posting"
	StatusPosted    = "posted"
	StatusFailed    = "failed"
)

type Account struct {
	ID       int64             `json:"id"`
	Provider string            `json:"provider"`
	Name     string            `json:"name"`
	creds    map[string]string `json:"-"`
}

type Post struct {
	ID          int64    `json:"id"`
	AccountID   int64    `json:"account_id"`
	Account     string   `json:"account"`
	Text        string   `json:"text"`
	Media       []string `json:"media"`
	ScheduledAt int64    `json:"scheduled_at"`
	Status      string   `json:"status"`
	URL         string   `json:"url"`
	Error       string   `json:"error"`
	Source      string   `json:"source"`
	user        string
}

type Routine struct {
	ID           int64  `json:"id"`
	AccountID    int64  `json:"account_id"`
	Account      string `json:"account"`
	Folder       string `json:"folder"`
	Instructions string `json:"instructions"`
	Cron         string `json:"cron"`
	Review       bool   `json:"review"`
	LastRun      int64  `json:"last_run"`
	user         string
	token        string
	used         []string
}

func encrypt(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return EncryptString(env.SECRET_KEY_DERIVATE_FOR_USER, string(b))
}

func decrypt(s string, v any) error {
	str, err := DecryptString(env.SECRET_KEY_DERIVATE_FOR_USER, s)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(str), v)
}

// ---------------------------------------------------------------- accounts

func addAccount(user, provider, name string, creds map[string]string) (int64, error) {
	enc, err := encrypt(creds)
	if err != nil {
		return 0, err
	}
	res, err := db.Exec(`INSERT INTO social_accounts(user, provider, name, creds, created_at) VALUES(?,?,?,?,?)`,
		user, provider, name, enc, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func removeAccount(user string, id int64) error {
	_, err := db.Exec(`DELETE FROM social_accounts WHERE user = ? AND id = ?`, user, id)
	db.Exec(`DELETE FROM social_routines WHERE user = ? AND account_id = ?`, user, id)
	return err
}

func listAccounts(user string) []Account {
	out := []Account{}
	if db == nil {
		return out
	}
	rows, err := db.Query(`SELECT id, provider, name FROM social_accounts WHERE user = ? ORDER BY id`, user)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a Account
		if rows.Scan(&a.ID, &a.Provider, &a.Name) == nil {
			out = append(out, a)
		}
	}
	return out
}

func getAccount(user string, id int64) (Account, error) {
	var (
		a   Account
		enc string
	)
	err := db.QueryRow(`SELECT id, provider, name, creds FROM social_accounts WHERE user = ? AND id = ?`, user, id).
		Scan(&a.ID, &a.Provider, &a.Name, &enc)
	if err != nil {
		return a, ErrNotFound
	}
	a.creds = map[string]string{}
	return a, decrypt(enc, &a.creds)
}

// findAccount matches what the model or the user typed: an id, a name or a provider
func findAccount(user, ref string) (Account, error) {
	ref = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ref), "#"))
	accounts := listAccounts(user)
	for _, a := range accounts {
		if ref == strings.ToLower(a.Name) || ref == jsonNumber(a.ID) {
			return getAccount(user, a.ID)
		}
	}
	var match []Account
	for _, a := range accounts {
		if strings.Contains(strings.ToLower(a.Name), ref) || a.Provider == ref {
			match = append(match, a)
		}
	}
	if len(match) == 1 {
		return getAccount(user, match[0].ID)
	} else if len(match) > 1 {
		return Account{}, NewError("several accounts match '"+ref+"', use the account id", 400)
	}
	return Account{}, NewError("no social account matches '"+ref+"'. Accounts are connected from the 🔗 button of the assistant", 404)
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// ---------------------------------------------------------------- posts

func createPost(p Post) (int64, error) {
	media, _ := json.Marshal(p.Media)
	res, err := db.Exec(`INSERT INTO social_posts(user, account_id, text, media, scheduled_at, status, source, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		p.user, p.AccountID, p.Text, string(media), p.ScheduledAt, p.Status, p.Source, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const postColumns = `p.id, p.user, p.account_id, COALESCE(a.name, '?'), p.text, p.media, p.scheduled_at, p.status, p.url, p.error, p.source`

func scanPosts(rows *sql.Rows, err error) []Post {
	out := []Post{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			p     Post
			media string
		)
		if rows.Scan(&p.ID, &p.user, &p.AccountID, &p.Account, &p.Text, &media, &p.ScheduledAt, &p.Status, &p.URL, &p.Error, &p.Source) == nil {
			json.Unmarshal([]byte(media), &p.Media)
			out = append(out, p)
		}
	}
	return out
}

func listPosts(user string, limit int) []Post {
	return scanPosts(db.Query(`SELECT `+postColumns+` FROM social_posts p LEFT JOIN social_accounts a ON a.id = p.account_id
		WHERE p.user = ? ORDER BY CASE WHEN p.status IN ('draft','scheduled') THEN 0 ELSE 1 END, p.scheduled_at DESC, p.id DESC LIMIT ?`, user, limit))
}

func getPost(user string, id int64) (Post, error) {
	posts := scanPosts(db.Query(`SELECT `+postColumns+` FROM social_posts p LEFT JOIN social_accounts a ON a.id = p.account_id
		WHERE p.user = ? AND p.id = ?`, user, id))
	if len(posts) == 0 {
		return Post{}, ErrNotFound
	}
	return posts[0], nil
}

func duePosts(now time.Time) []Post {
	return scanPosts(db.Query(`SELECT `+postColumns+` FROM social_posts p LEFT JOIN social_accounts a ON a.id = p.account_id
		WHERE p.status = ? AND p.scheduled_at <= ?`, StatusScheduled, now.Unix()))
}

// approvePost moves a draft to the publishing queue, publishing right away when no date was set
func approvePost(user string, id int64) error {
	res, err := db.Exec(`UPDATE social_posts SET status = ?, scheduled_at = MAX(scheduled_at, ?) WHERE user = ? AND id = ? AND status = ?`,
		StatusScheduled, time.Now().Unix(), user, id, StatusDraft)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NewError("this post isn't a draft anymore", 409)
	}
	return nil
}

func cancelPost(user string, id int64) error {
	p, err := getPost(user, id)
	if err != nil {
		return err
	}
	if p.Status != StatusDraft && p.Status != StatusScheduled && p.Status != StatusFailed {
		return NewError("only drafts, scheduled and failed posts can be cancelled", 409)
	}
	if _, err := db.Exec(`DELETE FROM social_posts WHERE user = ? AND id = ?`, user, id); err != nil {
		return err
	}
	cleanupMedia(p.Media)
	return nil
}

// claimPost makes sure a post is only published once
func claimPost(id int64) bool {
	res, err := db.Exec(`UPDATE social_posts SET status = ? WHERE id = ? AND status = ?`, StatusPosting, id, StatusScheduled)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func finishPost(id int64, url string, perr error) {
	if perr != nil {
		db.Exec(`UPDATE social_posts SET status = ?, error = ? WHERE id = ?`, StatusFailed, perr.Error(), id)
		return
	}
	db.Exec(`UPDATE social_posts SET status = ?, url = ?, error = '' WHERE id = ?`, StatusPosted, url, id)
}

// ---------------------------------------------------------------- routines

func createRoutine(r Routine) (int64, error) {
	res, err := db.Exec(`INSERT INTO social_routines(user, account_id, folder, instructions, cron, review, token, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		r.user, r.AccountID, r.Folder, r.Instructions, r.Cron, r.Review, r.token, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func listRoutines(user string) []Routine {
	q := `SELECT r.id, r.user, r.account_id, COALESCE(a.name, '?'), r.folder, r.instructions, r.cron, r.review, r.token, r.used, r.last_run
		FROM social_routines r LEFT JOIN social_accounts a ON a.id = r.account_id`
	var (
		rows *sql.Rows
		err  error
	)
	if user == "" {
		rows, err = db.Query(q)
	} else {
		rows, err = db.Query(q+` WHERE r.user = ? ORDER BY r.id`, user)
	}
	out := []Routine{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			r    Routine
			used string
		)
		if rows.Scan(&r.ID, &r.user, &r.AccountID, &r.Account, &r.Folder, &r.Instructions, &r.Cron, &r.Review, &r.token, &used, &r.LastRun) == nil {
			json.Unmarshal([]byte(used), &r.used)
			out = append(out, r)
		}
	}
	return out
}

func deleteRoutine(user string, id int64) error {
	res, err := db.Exec(`DELETE FROM social_routines WHERE user = ? AND id = ?`, user, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func markRoutineRun(r Routine, usedFile string) {
	if usedFile != "" {
		r.used = append(r.used, usedFile)
	}
	used, _ := json.Marshal(r.used)
	db.Exec(`UPDATE social_routines SET used = ?, last_run = ? WHERE id = ?`, string(used), time.Now().Unix(), r.ID)
}

// ---------------------------------------------------------------- media staging

const (
	maxMediaSize = 50 << 20 // images and anything loaded in memory
	maxVideoSize = 4 << 30  // videos are streamed from disk
)

var mediaNameRegex = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|gif|webp|mp4|mov|m4v|webm)$`)

var mediaDir = func() string {
	dir := GetAbsolutePath(DB_PATH, "ai_social_media")
	os.MkdirAll(dir, 0700)
	return dir
}

func stageMedia(name string, b []byte) (string, error) {
	return stageReader(name, bytes.NewReader(b), int64(len(b)))
}

// stageReader copies media in the staging area without holding it in memory
func stageReader(name string, r io.Reader, limit int64) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "jpeg" {
		ext = "jpg"
	}
	if !mediaNameRegex.MatchString(strings.Repeat("0", 32) + "." + ext) {
		return "", NewError("unsupported media type ."+ext+", use jpg, png, gif, webp, mp4, mov, m4v or webm", 400)
	}
	rnd := make([]byte, 16)
	rand.Read(rnd)
	staged := hex.EncodeToString(rnd) + "." + ext
	f, err := os.OpenFile(mediaPath(staged), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = NewError(fmt.Sprintf("%s is too large (max %s)", name, humanSize(limit)), 400)
	}
	if err != nil {
		os.Remove(mediaPath(staged))
		return "", err
	}
	return staged, nil
}

func mediaPath(staged string) string {
	return filepath.Join(mediaDir(), staged)
}

func readMedia(staged string) ([]byte, error) {
	if !mediaNameRegex.MatchString(staged) {
		return nil, ErrNotValid
	}
	return os.ReadFile(filepath.Join(mediaDir(), staged))
}

func cleanupMedia(media []string) {
	for _, m := range media {
		if mediaNameRegex.MatchString(m) {
			os.Remove(filepath.Join(mediaDir(), m))
		}
	}
}

func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

func mimeOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".m4v":
		return "video/x-m4v"
	case ".webm":
		return "video/webm"
	}
	return "application/octet-stream"
}

// toJPEG re-encodes an image, shrinking it until it fits under maxBytes
func toJPEG(b []byte, maxBytes int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, NewError("cannot decode image: "+err.Error(), 400)
	}
	for scale := 1.0; scale > 0.1; scale *= 0.75 {
		out := img
		if scale < 1 {
			bounds := img.Bounds()
			dst := image.NewRGBA(image.Rect(0, 0, int(float64(bounds.Dx())*scale), int(float64(bounds.Dy())*scale)))
			draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
			out = dst
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 85}); err != nil {
			return nil, err
		}
		if maxBytes <= 0 || buf.Len() <= maxBytes {
			return buf.Bytes(), nil
		}
	}
	return nil, NewError("image is too large", 400)
}
