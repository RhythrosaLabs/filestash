package plg_theme_sensorium

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

func TestDarkModePatchApplies(t *testing.T) {
	files, _, err := gitdiff.Parse(bytes.NewReader(DARKMODE))
	if err != nil || len(files) != 1 {
		t.Fatalf("cannot parse patch: %v", err)
	}
	orig, err := os.ReadFile("../../../public/assets/boot/ctrl_boot_frontoffice.js")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := gitdiff.Apply(&out, bytes.NewReader(orig), files[0]); err != nil {
		t.Fatalf("patch does not apply: %v", err)
	}
	if !strings.Contains(out.String(), `document.body.classList.add("dark-mode"); // Sensorium theme`) {
		t.Fatal("dark mode isn't forced")
	}
}

func TestThemeCSS(t *testing.T) {
	if strings.Count(CSS, "{") != strings.Count(CSS, "}") {
		t.Fatal("unbalanced braces in theme.css")
	}
	if !bytes.HasPrefix(LOGO, []byte("<svg")) {
		t.Fatal("logo must be an svg for the favicon mime detection")
	}
}
