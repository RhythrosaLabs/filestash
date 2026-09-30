package plg_widget_ai

import (
	"fmt"
	"net/http"
	"strings"

	. "github.com/mickael-kerjean/filestash/server/common"
)

// agentConnectHandler hands out what an external agent (DeepSeek Harness,
// Hermes Agent, Claude Desktop, ...) needs to use this storage through the MCP
// plugin: the endpoint and a bearer token for the current session
func agentConnectHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	if !Config.Get("features.mcp.enable").Bool() {
		SendErrorResult(res, NewError("Enable the MCP plugin first: admin > settings > features > mcp, then restart Filestash", 400))
		return
	}
	token, err := encrypt(ctx.Session)
	if err != nil {
		SendErrorResult(res, err)
		return
	}
	scheme := "http"
	if req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	base := scheme + "://" + req.Host + WithBase("")
	url := base + "/mcp"
	dsh := fmt.Sprintf(`# DeepSeek Harness: save as filestash.cordis.yml and run
#   npx @deepseek-ai/dsh web --patch "$PWD/filestash.cordis.yml"
- insert:
    - id: mcp-filestash
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: filestash
        transport: streamable-http
        url: %s
        headers:
          Authorization: Bearer %s
`, url, token)
	generic := fmt.Sprintf(`{
  "mcpServers": {
    "filestash": {
      "type": "http",
      "url": "%s",
      "headers": { "Authorization": "Bearer %s" }
    }
  }
}`, url, token)
	SendSuccessResult(res, map[string]string{
		"url":            url,
		"sse_url":        base + "/sse",
		"token":          token,
		"dsh_patch":      dsh,
		"generic_config": generic,
	})
}
