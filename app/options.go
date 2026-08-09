package app

import (
	"embed"

	"github.com/Miniature-Pug/perch/internal/registry"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// appIcon is the window and taskbar icon, embedded into the binary and handed to
// GTK through options.Linux.Icon. Replace app/appicon.png to change it.
//
//go:embed appicon.png
var appIcon []byte

// fileDropOptions configures Wails native file drop. EnableFileDrop registers
// the GTK drag-data-received / drag-drop handlers (see
// vendor/.../linux/window.c onDragDataReceived) that resolve each dropped file's
// ABSOLUTE path via g_filename_from_uri and deliver it to the frontend through
// the runtime OnFileDrop event ("wails:file-drop"). This is the only way to get
// a real path on WebKitGTK — the DOM drop event's File objects expose no path,
// so the frontend must consume the OnFileDrop paths (see lib/osFileDrop.ts).
//
// DisableWebViewDrop is deliberately left false. On Linux it calls
// gtk_drag_dest_unset, which removes the webview's drag destination and would
// stop the drag-data-received / drag-drop signals from ever firing — the two
// options are mutually exclusive for the file-drop-with-paths use case. WebKit
// navigating to a dropped file (Wails issue #3686) is instead prevented by the
// Wails runtime's own window-level preventDefault on dragover/drop, which it
// installs the moment the frontend registers OnFileDrop, backed by the
// drop-zone's own preventDefault handlers.
func fileDropOptions() *options.DragAndDrop {
	return &options.DragAndDrop{EnableFileDrop: true}
}

const (
	appTitle            = "perch"
	defaultWindowWidth  = 1280
	defaultWindowHeight = 800

	// singleInstanceID is the stable unique identifier passed to
	// options.SingleInstanceLock. It must never change between releases so the
	// lock stays consistent across upgrades.
	//
	// MUST be D-Bus-safe: on Linux, Wails builds the bus name
	// "org.wails_app_<id>.SingleInstance", replacing only "-" and "." with "_"
	// (see vendor/.../linux/single_instance.go) — it does NOT sanitize "/".
	// A bus-name element accepts only [A-Za-z0-9_], so any "/" yields an invalid
	// name; RequestName then fails silently and single-instance/attach is a
	// no-op. Keep this a dotted reverse-DNS string with no slashes.
	singleInstanceID = "com.miniature-pug.perch"
)

// defaultWindowBg is the webview background painted before the SPA mounts and
// behind any chrome not yet styled. Matches the default (gruvbox) theme's
// --perch-bg (#282828) so launch shows no white flash.
var defaultWindowBg = options.RGBA{R: 40, G: 40, B: 40, A: 255}

// Run launches the Wails desktop app. assets is the embedded SPA (from the repo
// root package). Production builds expose NO listening TCP port: IPC travels over
// the WebKit2GTK script-message channel and assets are served via the wails://
// custom URI scheme. The dev-only ws://localhost:34115 reload socket is compiled
// in ONLY under `-tags dev` (Wails injects it behind its own build tag), so
// release builds have no network surface.
func Run(assets embed.FS, roots []string) error {
	// Fail fast with an actionable message if the binary embeds the placeholder
	// frontend stub (e.g. from `make install` / `go install`, which do not
	// rebuild the frontend) rather than opening a blank window.
	if err := checkFrontendIndex(assets); err != nil {
		return err
	}
	store, err := registry.Load(registry.DefaultConfigDir())
	if err != nil {
		return err
	}
	app := NewApp(store, roots)
	return wails.Run(&options.App{
		Title:            appTitle,
		Width:            defaultWindowWidth,
		Height:           defaultWindowHeight,
		BackgroundColour: &defaultWindowBg,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// Linux.Icon sets the GTK window/taskbar icon. ProgramName sets the WM
		// class (g_set_prgname), which the installed perch.desktop matches via
		// StartupWMClass so the app-switcher entry picks up the same icon.
		Linux: &linux.Options{
			Icon:        appIcon,
			ProgramName: appTitle,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		DragAndDrop: fileDropOptions(),
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               singleInstanceID,
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
	})
}
