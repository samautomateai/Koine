// Wails v2 desktop app. It starts `koine --mode rpc` as a child process and talks to it through core/rpcclient (REQ-FE-002, REQ-FE-003,
// ADR 0009), so it needs the koine binary (KOINE_BIN, next to this program, or on PATH). The page is plain HTML/JS (no npm build step) and
// talks to Go through the bound Backend and the "koine:update" event.
//
//	cd desktop && go mod tidy && wails dev     (or: wails build; on Linux add -tags webkit2_41 with WebKitGTK 4.1)
package main

import (
	"context"
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"koine-desktop/bridge"
	"koine/core/rpcclient"
)

//go:embed all:frontend
var assets embed.FS

// app holds what the Wails callbacks share.
type app struct {
	b    *bridge.Backend
	proc *rpcclient.Process
}

func (a *app) startup(ctx context.Context) {
	a.b.SetEmit(func(name string, data ...any) { runtime.EventsEmit(ctx, name, data...) })
	proc, err := rpcclient.Start(ctx, rpcclient.Options{Dir: a.b.Cwd})
	if err != nil {
		a.b.Fail(err)
		return
	}
	a.proc = proc
	go a.b.Attach(proc, proc.Events())
}

func (a *app) shutdown(context.Context) {
	if a.proc != nil {
		_ = a.proc.Close()
	}
}

func main() {
	cwd := os.Getenv("KOINE_CWD")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	a := &app{b: bridge.NewBackend(func(string, ...any) {}, cwd)}
	err := wails.Run(&options.App{
		Title:            "koine",
		Width:            1100,
		Height:           760,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 18, G: 18, B: 20, A: 1},
		OnStartup:        a.startup,
		OnShutdown:       a.shutdown,
		Bind:             []interface{}{a.b},
	})
	if err != nil {
		println("error:", err.Error())
	}
}
