package plg_theme_sensorium

import (
	_ "embed"

	. "github.com/mickael-kerjean/filestash/server/common"
)

// Sensorium look: dark, glassy surfaces, gradient accents. Pure CSS on top of
// Filestash's design tokens so every page picks it up, admin included.

//go:embed theme.css
var CSS string

//go:embed darkmode.diff
var DARKMODE []byte

//go:embed logo.svg
var LOGO []byte

func init() {
	Hooks.Register.Onload(func() {
		if PluginEnable() {
			Hooks.Register.CSS(CSS, WithID("plg_theme_sensorium"))
			Hooks.Register.StaticPatch(DARKMODE, WithID("plg_theme_sensorium"))
			Hooks.Register.Favicon(LOGO)
		}
	})
	Hooks.Register.OnConfig(func() {
		if PluginEnable() {
			Hooks.Register.CSS(CSS, WithID("plg_theme_sensorium"))
			Hooks.Register.StaticPatch(DARKMODE, WithID("plg_theme_sensorium"))
		} else {
			Hooks.Register.CSS("", WithID("plg_theme_sensorium"))
			Hooks.Register.StaticPatch([]byte(""), WithID("plg_theme_sensorium"))
		}
	})
}

var PluginEnable = func() bool {
	return Config.Get("features.theme.sensorium").Schema(func(f *FormElement) *FormElement {
		if f == nil {
			f = &FormElement{}
		}
		f.Name = "sensorium"
		f.Type = "boolean"
		f.Default = true
		f.Description = "Dark Sensorium theme"
		return f
	}).Bool()
}
