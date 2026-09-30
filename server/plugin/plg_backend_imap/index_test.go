package plg_backend_imap

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"
	. "github.com/mickael-kerjean/filestash/server/common"
)

func TestImap(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := server.New(memory.New())
	s.AllowInsecureAuth = true
	go s.Serve(l)
	defer s.Close()
	host, port, _ := net.SplitHostPort(l.Addr().String())
	params := map[string]string{"hostname": host, "port": port, "security": "none", "username": "username"}
	app := &App{Context: context.Background()}

	params["password"] = "wrong"
	if _, err := (Imap{}).Init(params, app); err != ErrAuthenticationFailed {
		t.Fatalf("expected auth failure, got %v", err)
	}
	params["password"] = "password"
	b, err := (Imap{}).Init(params, app)
	if err != nil {
		t.Fatal(err)
	}

	root, err := b.Ls("/")
	if err != nil || len(root) != 1 || root[0].Name() != "INBOX" || !root[0].IsDir() {
		t.Fatalf("unexpected root: %v %v", root, err)
	}
	inbox, err := b.Ls("/INBOX/")
	if err != nil || len(inbox) != 1 {
		t.Fatalf("unexpected inbox: %v %v", inbox, err)
	}
	name := inbox[0].Name()
	if !strings.HasSuffix(name, "A little message, just for you [6].eml") {
		t.Fatalf("unexpected name %q", name)
	}

	r, err := b.Cat("/INBOX/" + name)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r)
	r.Close()
	if !strings.Contains(string(body), "Hi there :)") {
		t.Fatalf("unexpected body %q", body)
	}
	if st, err := b.Stat("/INBOX/" + name); err != nil || st.Size() != int64(len(body)) {
		t.Fatalf("stat: %v %v", st, err)
	}
	if _, err := b.Cat("/INBOX/nope [999].eml"); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}

	if err := b.Mkdir("/Archive/"); err != nil {
		t.Fatal(err)
	}
	if err := b.Mv("/INBOX/"+name, "/INBOX/renamed.eml"); err == nil {
		t.Fatal("renaming an email should fail")
	}
	if err := b.Mv("/INBOX/"+name, "/Archive/"+name); err != nil {
		t.Fatal(err)
	}
	if files, _ := b.Ls("/INBOX/"); len(files) != 0 {
		t.Fatalf("inbox should be empty: %v", files)
	}
	archive, err := b.Ls("/Archive/")
	if err != nil || len(archive) != 1 {
		t.Fatalf("archive: %v %v", archive, err)
	}

	eml := "From: me@example.org\r\nSubject: Saved draft\r\n\r\nhello\r\n"
	if err := b.Save("/Archive/draft.eml", strings.NewReader(eml)); err != nil {
		t.Fatal(err)
	}
	archive, _ = b.Ls("/Archive/")
	if len(archive) != 2 {
		t.Fatalf("expected 2 emails, got %d", len(archive))
	}
	for _, f := range archive {
		if err := b.Rm("/Archive/" + f.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if archive, _ = b.Ls("/Archive/"); len(archive) != 0 {
		t.Fatalf("archive should be empty: %v", archive)
	}
	if err := b.Mv("/Archive/", "/Old/"); err != nil {
		t.Fatal(err)
	}
	if err := b.Rm("/Old/"); err != nil {
		t.Fatal(err)
	}
	if root, _ = b.Ls("/"); len(root) != 1 {
		t.Fatalf("expected only INBOX left: %v", root)
	}

	// cached connection is reused
	b2, err := (Imap{}).Init(params, app)
	if err != nil || b2 != b {
		t.Fatalf("expected the cached connection")
	}
}
