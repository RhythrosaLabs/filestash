package plg_backend_rclone

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"

	"golang.org/x/crypto/bcrypt"
)

// Rclone exposes any remote declared in the server's rclone config (iCloud Drive,
// OneDrive, Box, extra Dropbox/Google Drive accounts, ...) as a Filestash backend.
// The rclone config holds credentials so, like the local backend, a connection
// is only granted to someone who knows RCLONE_BACKEND_SECRET or the admin password.
//
// Environment:
//   - RCLONE_BINARY: path of the rclone executable (default: "rclone" from $PATH)
//   - RCLONE_CONFIG: rclone config file, read natively by rclone
//   - RCLONE_BACKEND_SECRET: optional shared secret accepted in place of the admin password
func init() {
	bin := os.Getenv("RCLONE_BINARY")
	if bin == "" {
		bin = "rclone"
	}
	Backend.Register("rclone", &Rclone{bin: bin, secret: os.Getenv("RCLONE_BACKEND_SECRET")})
}

var remoteNameRegex = regexp.MustCompile(`^[A-Za-z0-9_+@][A-Za-z0-9_.+@ -]*$`)

type Rclone struct {
	bin    string
	secret string
	remote string
	root   string
	ctx    context.Context
}

func (this Rclone) Init(params map[string]string, app *App) (IBackend, error) {
	if this.secret != "" && params["password"] == this.secret {
		// ok
	} else if err := bcrypt.CompareHashAndPassword(
		[]byte(Config.Get("auth.admin").String()),
		[]byte(params["password"]),
	); err != nil {
		return nil, ErrAuthenticationFailed
	}
	ctx := context.Background()
	if app != nil && app.Context != nil {
		ctx = app.Context
	}
	remote := strings.TrimSuffix(strings.TrimSpace(params["remote"]), ":")
	if remoteNameRegex.MatchString(remote) == false {
		return nil, NewError("Invalid remote name", 400)
	}
	backend := &Rclone{
		bin:    this.bin,
		remote: remote,
		root:   strings.Trim(filepath.ToSlash(filepath.Clean("/"+params["path"])), "/"),
		ctx:    ctx,
	}
	// only remotes declared in the rclone config are allowed: this rules out
	// on the fly connection strings such as ":local:" that would expose the server
	out, err := backend.run(nil, "listremotes")
	if err != nil {
		Log.Warning("plg_backend_rclone::init action=listremotes err=%s", err.Error())
		return nil, ErrMissingDependency
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSuffix(strings.TrimSpace(line), ":") == remote {
			return backend, nil
		}
	}
	return nil, NewError("Unknown rclone remote", 404)
}

func (this Rclone) LoginForm() Form {
	return Form{
		Elmnts: []FormElement{
			{
				Name:  "type",
				Type:  "hidden",
				Value: "rclone",
			},
			{
				Name:        "remote",
				Type:        "text",
				Placeholder: "Remote name*",
			},
			{
				Name:        "password",
				Type:        "password",
				Placeholder: "Password*",
			},
			{
				Name:        "advanced",
				Type:        "enable",
				Placeholder: "Advanced",
				Target:      []string{"rclone_path"},
			},
			{
				Id:          "rclone_path",
				Name:        "path",
				Type:        "text",
				Placeholder: "Path",
			},
		},
	}
}

func (this Rclone) Home() (string, error) {
	return "/", nil
}

type lsItem struct {
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

func (this Rclone) toFile(item lsItem) File {
	f := File{FName: item.Name, FType: "file", FSize: item.Size, FTime: item.ModTime.Unix()}
	if item.IsDir {
		f.FType = "directory"
		f.FSize = -1
	}
	return f
}

func (this Rclone) Ls(path string) ([]os.FileInfo, error) {
	out, err := this.run(nil, "lsjson", "--no-mimetype", this.target(path))
	if err != nil {
		return nil, err
	}
	var items []lsItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, ErrNotValid
	}
	files := make([]os.FileInfo, 0, len(items))
	for _, item := range items {
		files = append(files, this.toFile(item))
	}
	return files, nil
}

func (this Rclone) Stat(path string) (os.FileInfo, error) {
	out, err := this.run(nil, "lsjson", "--stat", "--no-mimetype", this.target(path))
	if err != nil {
		return nil, err
	}
	var item lsItem
	if err := json.Unmarshal(out, &item); err != nil {
		return nil, ErrNotValid
	}
	if item.Name == "" {
		item.Name = filepath.Base(strings.TrimSuffix(path, "/"))
	}
	return this.toFile(item), nil
}

func (this Rclone) Cat(path string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(this.ctx, this.bin, "cat", this.target(path))
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, ErrMissingDependency
	}
	return &catReader{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func (this Rclone) Mkdir(path string) error {
	_, err := this.run(nil, "mkdir", this.target(path))
	return err
}

func (this Rclone) Rm(path string) error {
	if strings.HasSuffix(path, "/") {
		_, err := this.run(nil, "purge", this.target(path))
		return err
	}
	_, err := this.run(nil, "deletefile", this.target(path))
	return err
}

func (this Rclone) Mv(from, to string) error {
	if strings.HasSuffix(from, "/") {
		_, err := this.run(nil, "move", "--delete-empty-src-dirs", this.target(from), this.target(to))
		return err
	}
	_, err := this.run(nil, "moveto", this.target(from), this.target(to))
	return err
}

func (this Rclone) Save(path string, content io.Reader) error {
	_, err := this.run(content, "rcat", this.target(path))
	return err
}

func (this Rclone) Touch(path string) error {
	_, err := this.run(nil, "touch", this.target(path))
	return err
}

// target converts a Filestash path into an rclone "remote:path" that can't escape the configured root
func (this Rclone) target(path string) string {
	p := strings.Trim(filepath.ToSlash(filepath.Clean("/"+path)), "/")
	if this.root != "" {
		p = strings.Trim(this.root+"/"+p, "/")
	}
	return this.remote + ":" + p
}

func (this Rclone) run(stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(this.ctx, this.bin, args...)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, ErrMissingDependency
		}
		return nil, toError(stderr.String())
	}
	return stdout.Bytes(), nil
}

func toError(stderr string) error {
	msg := strings.ToLower(stderr)
	switch {
	case strings.Contains(msg, "not found"), strings.Contains(msg, "doesn't exist"):
		return ErrNotFound
	case strings.Contains(msg, "permission denied"), strings.Contains(msg, "forbidden"):
		return ErrPermissionDenied
	case strings.Contains(msg, "already exists"):
		return ErrConflict
	}
	Log.Debug("plg_backend_rclone::run stderr=%s", strings.TrimSpace(stderr))
	return ErrFilesystemError
}

// catReader streams "rclone cat" and surfaces the process error instead of a silent EOF
type catReader struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *bytes.Buffer
	done   bool
	err    error
}

func (this *catReader) Read(p []byte) (int, error) {
	n, err := this.stdout.Read(p)
	if err == io.EOF {
		if werr := this.wait(); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (this *catReader) wait() error {
	if this.done == false {
		this.done = true
		if err := this.cmd.Wait(); err != nil {
			this.err = toError(this.stderr.String())
		}
	}
	return this.err
}

func (this *catReader) Close() error {
	if this.done == false && this.cmd.Process != nil {
		this.cmd.Process.Kill()
		this.stdout.Close()
		this.wait()
		return nil
	}
	return this.err
}
