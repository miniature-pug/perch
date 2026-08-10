package app

import (
	"embed"

	"github.com/miniature-pug/perch/internal/registry"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// appIcon is the window and taskbar icon. The binary embeds appIcon, and
// passes appIcon to GTK through options.Linux.Icon. To change the icon,
// replace app/appicon.png.
//
//go:embed appicon.png
var appIcon []byte

// fileDropOptions configures the Wails native file drop. EnableFileDrop
// registers the GTK drag-data-received and drag-drop handlers (see
// vendor/.../linux/window.c onDragDataReceived). These handlers resolve each
// dropped file's absolute path through g_filename_from_uri, and deliver the
// path to the frontend through the runtime OnFileDrop event
// ("wails:file-drop"). This is the only way to get a real path on
// WebKitGTK. The DOM drop event's File objects expose no path, so the
// frontend must read the OnFileDrop paths (see lib/osFileDrop.ts).
//
// DisableWebViewDrop stays false on purpose. On Linux, setting
// DisableWebViewDrop to true calls gtk_drag_dest_unset. This call removes
// the webview's drag destination, and would stop the drag-data-received and
// drag-drop signals from firing. So DisableWebViewDrop and EnableFileDrop
// are mutually exclusive for the file-drop-with-paths use case. Instead,
// perch prevents WebKit from navigating to a dropped file (Wails issue
// #3686) through the Wails runtime's own window-level preventDefault on
// dragover and drop. The Wails runtime installs this handler the moment the
// frontend registers OnFileDrop, and the drop zone's own preventDefault
// handlers add backup support.
func fileDropOptions() *options.DragAndDrop {
	return &options.DragAndDrop{EnableFileDrop: true}
}

const (
	appTitle            = "perch"
	defaultWindowWidth  = 1280
	defaultWindowHeight = 800

	// singleInstanceID is the stable unique identifier passed to
	// options.SingleInstanceLock. singleInstanceID must never change between
	// releases, so the lock stays consistent across upgrades.
	//
	// singleInstanceID must be D-Bus-safe. On Linux, Wails builds the bus
	// name "org.wails_app_<id>.SingleInstance", and replaces only "-" and
	// "." with "_" (see vendor/.../linux/single_instance.go). Wails does not
	// sanitize "/". A bus-name element accepts only [A-Za-z0-9_], so any "/"
	// gives an invalid name. RequestName then fails silently, and
	// single-instance and attach become a no-op. Keep singleInstanceID a
	// dotted reverse-DNS string with no slashes.
	singleInstanceID = "com.miniature-pug.perch"
)

// defaultWindowBg is the webview background. perch paints this background
// before the SPA mounts, and behind any chrome not yet styled.
// defaultWindowBg matches the default (gruvbox) theme's --perch-bg
// (#282828), so launch shows no white flash.
var defaultWindowBg = options.RGBA{R: 40, G: 40, B: 40, A: 255}

// Run launches the Wails desktop app. assets is the embedded SPA from the
// repo root package. Production builds expose no listening TCP port. IPC
// travels over the WebKit2GTK script-message channel, and perch serves
// assets through the wails:// custom URI scheme. The dev-only
// ws://localhost:34115 reload socket compiles in only under the `-tags dev`
// build tag (Wails injects the socket behind its own build tag), so release
// builds have no network surface.
func Run(assets embed.FS, roots []string) error {
	// Fail fast with a clear message when the binary embeds the placeholder
	// frontend stub, for example from `make install` or `go install`, which
	// do not rebuild the frontend. This is better than opening a blank
	// window.
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
		// Linux.Icon sets the GTK window and taskbar icon. ProgramName sets
		// the WM class through g_set_prgname. The installed perch.desktop
		// file matches this WM class through StartupWMClass, so the
		// app-switcher entry shows the same icon.
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
