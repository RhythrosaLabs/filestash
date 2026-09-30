package plg_widget_ai

import (
	. "github.com/mickael-kerjean/filestash/server/common"
	"github.com/mickael-kerjean/filestash/server/pkg/config"
)

func init() {
	Hooks.Register.Onload(func() {
		PluginEnable()
		PluginBaseURL()
		PluginModel()
		PluginAPIKey()
		PluginMaxSteps()
	})
}

func setting(name string, fn func(f *FormElement)) config.ConfigElement {
	return Config.Get("features.ai." + name).Schema(func(f *FormElement) *FormElement {
		if f == nil {
			f = &FormElement{}
		}
		f.Name = name
		fn(f)
		return f
	})
}

var PluginEnable = func() bool {
	return setting("enable", func(f *FormElement) {
		f.Type = "enable"
		f.Target = []string{"ai_base_url", "ai_model", "ai_api_key", "ai_max_steps"}
		f.Description = "AI assistant that can search, analyse and organise your files"
		f.Default = false
	}).Bool()
}

var PluginBaseURL = func() string {
	return setting("base_url", func(f *FormElement) {
		f.Id = "ai_base_url"
		f.Type = "text"
		f.Description = "OpenAI compatible endpoint. Ollama: http://localhost:11434/v1 - DeepSeek: https://api.deepseek.com/v1 - OpenRouter: https://openrouter.ai/api/v1"
		f.Default = "http://localhost:11434/v1"
		f.Placeholder = "http://localhost:11434/v1"
	}).String()
}

var PluginModel = func() string {
	return setting("model", func(f *FormElement) {
		f.Id = "ai_model"
		f.Type = "text"
		f.Description = "Model with tool calling support, eg: hermes3, qwen3, llama3.1, deepseek-chat"
		f.Default = "qwen3:8b"
		f.Placeholder = "qwen3:8b"
	}).String()
}

var PluginAPIKey = func() string {
	return setting("api_key", func(f *FormElement) {
		f.Id = "ai_api_key"
		f.Type = "password"
		f.Description = "API key, leave empty for Ollama"
	}).String()
}

var PluginMaxSteps = func() int {
	return setting("max_steps", func(f *FormElement) {
		f.Id = "ai_max_steps"
		f.Type = "number"
		f.Description = "Maximum number of tool calls the assistant can chain for a single request"
		f.Default = 12
	}).Int()
}
