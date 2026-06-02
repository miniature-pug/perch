//go:build ignore

package main

import (
	"context"
	"embed"
	"fmt"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

//go:embed frontend/dist
var assets embed.FS

type App struct{}

func (a *App) OnDrop(x, y int, paths []string) {
	fmt.Printf("OnFileDrop: x=%d y=%d paths=%v\n", x, y, paths)
}

func main() {
	app := &App{}
	err := wails.Run(&options.App{
		Title:  "Spike 4 — OnFileDrop",
		Width:  640,
		Height: 480,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup: func(ctx context.Context) { fmt.Println("ready") },
		OnFileDrop: app.OnDrop,
		Linux: &linux.Options{
			WindowIsTranslucent: false,
		},
		// DisableWebViewDrop prevents WebKitGTK from intercepting OS drop events
		// before OnFileDrop can fire (issue #3686).
		DisableWebViewDrop: true,
		Bind:               []interface{}{app},
	})
	if err != nil {
		fmt.Println("Error:", err)
	}
}
