package plg_handler_mcp

import "testing"

func TestChroot(t *testing.T) {
	if b := withChroot(nil, map[string]string{"path": "/"}); b != nil {
		t.Fatal("a session on / must not be wrapped")
	}
	c := withChroot(nil, map[string]string{"path": "/home/me/files/"}).(chrootBackend)
	for in, want := range map[string]string{
		"/":                   "/home/me/files/",
		"/docs/":              "/home/me/files/docs/",
		"/docs/a.txt":         "/home/me/files/docs/a.txt",
		"/../../etc/passwd":   "/home/me/files/etc/passwd",
		"docs/../../../etc/":  "/home/me/files/etc/",
		"/docs/./sub/../b.md": "/home/me/files/docs/b.md",
	} {
		if got := c.p(in); got != want {
			t.Errorf("p(%q) = %q, want %q", in, got, want)
		}
	}
}
