// Wails v2 desktop app. The Go core runs in-process; the frontend is plain
// HTML/JS (no npm build step) and talks to Go through bound methods + events.
//
//	cd desktop && go mod tidy && wails dev     (or: wails build)
package main

import (
	"context"
	"embed"
	"os"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"koine/core/agent"
	"koine/core/app"
)

//go:embed all:frontend
var assets embed.FS

// Backend is bound to the frontend: window.go.main.Backend.*
type Backend struct {
	ctx    context.Context
	app    *app.App
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (b *Backend) startup(ctx context.Context) {
	b.ctx = ctx
	cwd := os.Getenv("KOINE_CWD")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	a, err := app.New(ctx, cwd)
	if err != nil {
		runtime.EventsEmit(ctx, "koine:event", agent.Event{Type: "error", Text: err.Error()})
		return
	}
	b.app = a
}

func (b *Backend) shutdown(context.Context) {
	if b.app != nil {
		b.app.Close()
	}
}

// Info returns tools/skills/agents/commands for the sidebar.
func (b *Backend) Info() app.Info {
	if b.app == nil {
		return app.Info{}
	}
	return b.app.Describe()
}

// Send runs a prompt (or slash command); progress streams as "koine:event".
func (b *Backend) Send(text string) {
	if b.app == nil {
		return
	}
	b.mu.Lock()
	if b.cancel != nil { // a run is in flight
		b.mu.Unlock()
		runtime.EventsEmit(b.ctx, "koine:event", agent.Event{Type: "notice", Text: "a run is already in progress"})
		return
	}
	ctx, cancel := context.WithCancel(b.ctx)
	b.cancel = cancel
	b.mu.Unlock()
	go func() {
		defer func() {
			b.mu.Lock()
			b.cancel = nil
			b.mu.Unlock()
			cancel()
			runtime.EventsEmit(b.ctx, "koine:done")
		}()
		_, err := b.app.HandleInput(ctx, text, func(e agent.Event) { runtime.EventsEmit(b.ctx, "koine:event", e) })
		if err != nil && ctx.Err() == nil {
			runtime.EventsEmit(b.ctx, "koine:event", agent.Event{Type: "error", Text: err.Error()})
		}
	}()
}

// Cancel interrupts the current run.
func (b *Backend) Cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
}

func main() {
	b := &Backend{}
	err := wails.Run(&options.App{
		Title:            "koine",
		Width:            1100,
		Height:           760,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 18, G: 18, B: 20, A: 1},
		OnStartup:        b.startup,
		OnShutdown:       b.shutdown,
		Bind:             []interface{}{b},
	})
	if err != nil {
		println("error:", err.Error())
	}
}
