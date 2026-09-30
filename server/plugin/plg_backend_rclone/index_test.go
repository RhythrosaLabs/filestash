package plg_backend_rclone

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/mickael-kerjean/filestash/server/common"
)

func TestTarget(t *testing.T) {
	b := Rclone{remote: "icloud", root: "Photos"}
	for in, want := range map[string]string{
		"/":              "icloud:Photos",
		"/a/b.txt":       "icloud:Photos/a/b.txt",
		"/dir/":          "icloud:Photos/dir",
		"/../../etc/pwd": "icloud:Photos/etc/pwd",
	} {
		if got := b.target(in); got != want {
			t.Errorf("target(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteName(t *testing.T) {
	for _, name := range []string{"icloud", "dropbox-work", "gdrive_2", "my drive"} {
		if !remoteNameRegex.MatchString(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", ":local", "-flag", "a:b", "x,y=z", "a/b"} {
		if remoteNameRegex.MatchString(name) {
			t.Errorf("%q should be rejected", name)
		}
	}
}

// integration test: runs only when an rclone binary is available
func TestIntegration(t *testing.T) {
	bin := os.Getenv("RCLONE_BINARY")
	if bin == "" {
		bin = "rclone"
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skip("rclone not installed")
	}
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	os.Mkdir(data, 0755)
	conf := filepath.Join(dir, "rclone.conf")
	os.WriteFile(conf, []byte("[test]\ntype = alias\nremote = "+data+"\n"), 0600)
	t.Setenv("RCLONE_CONFIG", conf)

	proto := &Rclone{bin: bin, secret: "s3cret"}
	if _, err := proto.Init(map[string]string{"remote": "test", "password": "nope"}, nil); err == nil {
		t.Fatal("expected auth failure")
	}
	if _, err := proto.Init(map[string]string{"remote": ":local", "password": "s3cret"}, nil); err == nil {
		t.Fatal("expected on the fly remote to be rejected")
	}
	if _, err := proto.Init(map[string]string{"remote": "missing", "password": "s3cret"}, nil); err == nil {
		t.Fatal("expected unknown remote to be rejected")
	}
	b, err := proto.Init(map[string]string{"remote": "test", "password": "s3cret"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.Mkdir("/docs/"))
	must(b.Save("/docs/hello.txt", strings.NewReader("hello world")))
	must(b.Touch("/empty.txt"))

	files, err := b.Ls("/docs/")
	must(err)
	if len(files) != 1 || files[0].Name() != "hello.txt" || files[0].Size() != 11 {
		t.Fatalf("unexpected ls: %+v", files)
	}
	root, err := b.Ls("/")
	must(err)
	if len(root) != 2 {
		t.Fatalf("expected 2 entries in root, got %d", len(root))
	}

	st, err := b.Stat("/docs/")
	must(err)
	if !st.IsDir() {
		t.Fatal("expected directory")
	}

	r, err := b.Cat("/docs/hello.txt")
	must(err)
	content, err := io.ReadAll(r)
	must(err)
	must(r.Close())
	if string(content) != "hello world" {
		t.Fatalf("unexpected content %q", content)
	}

	r, err = b.Cat("/nope.txt")
	must(err)
	if _, err = io.ReadAll(r); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	r.Close()
	if _, err := b.Stat("/nope.txt"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound on stat, got %v", err)
	}

	must(b.Mv("/docs/hello.txt", "/docs/renamed.txt"))
	must(b.Mv("/docs/", "/archive/"))
	if _, err := os.Stat(filepath.Join(data, "archive", "renamed.txt")); err != nil {
		t.Fatal("move did not happen")
	}
	if _, err := os.Stat(filepath.Join(data, "docs")); err == nil {
		t.Fatal("source dir should be gone after move")
	}

	must(b.Rm("/empty.txt"))
	must(b.Rm("/archive/"))
	left, _ := os.ReadDir(data)
	if len(left) != 0 {
		t.Fatalf("expected empty data dir, got %d entries", len(left))
	}
}
