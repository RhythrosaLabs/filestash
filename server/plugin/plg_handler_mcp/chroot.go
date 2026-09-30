package plg_handler_mcp

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/mickael-kerjean/filestash/server/pkg/core"
)

// chrootBackend confines MCP tools to the folder the session was opened on,
// like the web client does: "/" for the agent is the session's "path"
type chrootBackend struct {
	IBackend
	root string
}

func withChroot(b IBackend, session map[string]string) IBackend {
	root := strings.TrimSuffix(filepath.ToSlash(filepath.Clean("/"+session["path"])), "/")
	if root == "" {
		return b
	}
	return chrootBackend{b, root}
}

func (this chrootBackend) p(path string) string {
	clean := filepath.ToSlash(filepath.Clean("/" + path))
	if strings.HasSuffix(path, "/") && clean != "/" {
		clean += "/"
	}
	return this.root + clean
}

func (this chrootBackend) Ls(path string) ([]os.FileInfo, error) {
	return this.IBackend.Ls(this.p(path))
}
func (this chrootBackend) Stat(path string) (os.FileInfo, error) {
	return this.IBackend.Stat(this.p(path))
}
func (this chrootBackend) Cat(path string) (io.ReadCloser, error) {
	return this.IBackend.Cat(this.p(path))
}
func (this chrootBackend) Mkdir(path string) error { return this.IBackend.Mkdir(this.p(path)) }
func (this chrootBackend) Rm(path string) error    { return this.IBackend.Rm(this.p(path)) }
func (this chrootBackend) Mv(from, to string) error {
	return this.IBackend.Mv(this.p(from), this.p(to))
}
func (this chrootBackend) Save(path string, file io.Reader) error {
	return this.IBackend.Save(this.p(path), file)
}
func (this chrootBackend) Touch(path string) error { return this.IBackend.Touch(this.p(path)) }
