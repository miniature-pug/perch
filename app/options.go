package app

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// Run launches the Wails desktop app. assets is the embedded SPA (from the repo
// root package). Production builds expose NO listening TCP port: IPC travels over
// the WebKit2GTK script-message channel and assets are served via the wails://
// custom URI scheme. The dev-only ws://localhost:34115 reload socket is compiled
// in ONLY under `-tags dev` (Wails injects it behind its own build tag), so
// release builds have no network surface.
func Run(assets embed.FS, roots []string) error {
	app := NewApp(roots)
	return wails.Run(&options.App{
		Title:  "perch",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
}
