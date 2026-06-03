package app

import (
	"embed"

	"github.com/Miniature-Pug/perch/internal/registry"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// disableWebViewDropForSpike4 mitigates WebKitGTK hijacking OS file-drop events
// before OnFileDrop fires (Wails issue #3686). Spike 4 validated that this flag,
// combined with frontend preventDefault on dragover/drop, prevents the UI being
// replaced by the dropped file. If a future Wails release resolves #3686, set to
// false and remove the corresponding frontend listeners.
const disableWebViewDropForSpike4 = true

// Run launches the Wails desktop app. assets is the embedded SPA (from the repo
// root package). Production builds expose NO listening TCP port: IPC travels over
// the WebKit2GTK script-message channel and assets are served via the wails://
// custom URI scheme. The dev-only ws://localhost:34115 reload socket is compiled
// in ONLY under `-tags dev` (Wails injects it behind its own build tag), so
// release builds have no network surface.
func Run(assets embed.FS, roots []string) error {
	store, err := registry.Load(registry.DefaultConfigDir())
	if err != nil {
		return err
	}
	app := NewApp(store, roots)
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
		DragAndDrop: &options.DragAndDrop{
			DisableWebViewDrop: disableWebViewDropForSpike4,
		},
	})
}
