package plg_widget_ai

import (
	"fmt"
	"strings"

	. "github.com/mickael-kerjean/filestash/server/common"
	"github.com/mickael-kerjean/filestash/server/pkg/permissions"
)

// search_content goes through Filestash's own search engine: with the
// full text search plugin (plg_search_sqlitefts) that's an index of what's
// inside documents, not just their names
func init() {
	toolDefs = append(toolDefs, tool("search_content",
		"Search inside files (text, documents, ...) with the full text search index. Use it to find files by what they contain, search matches names only",
		[]string{"query"}, map[string]any{
			"query": param("query", "string", "Words to look for"),
			"path":  param("path", "string", "Where to search. Default: current directory"),
		}))
	extraTools["search_content"] = searchContentTool
}

func searchContentTool(sess *Session, args map[string]any) (string, error) {
	engine := Hooks.Get.SearchEngine()
	if engine == nil {
		return "", fmt.Errorf("no search engine is enabled, use the search tool instead")
	}
	if !permissions.CanRead(sess.ctx) {
		return "", ErrPermissionDenied
	}
	query := argStr(args, "query")
	if query == "" {
		return "", ErrNotValid
	}
	_, full, err := sess.resolve(argStr(args, "path"), true)
	if err != nil {
		return "", err
	}
	results, err := engine.Query(*sess.ctx, full, query)
	if err != nil {
		return "", err
	}
	chroot := sess.ctx.Session["path"]
	var b strings.Builder
	fmt.Fprintf(&b, "%d results for %q\n", len(results), query)
	for i, r := range results {
		if i >= maxSearchResult {
			fmt.Fprintf(&b, "... %d more\n", len(results)-i)
			break
		}
		p := r.Path()
		if chroot != "" {
			p = "/" + strings.TrimPrefix(p, strings.TrimSuffix(chroot, "/")+"/")
		}
		fmt.Fprintf(&b, "%s  %s\n", p, humanSize(r.Size()))
	}
	if len(results) == 0 {
		b.WriteString("(the index may still be building for new folders; the search tool matches names right away)\n")
	}
	return b.String(), nil
}
