// Wails v3 desktop app (ADR 0015). It starts `koine --mode rpc` as a child process and talks to it through core/rpcclient (REQ-FE-002,
// REQ-FE-003, ADR 0013), so it needs the koine binary (KOINE_BIN, next to this program, or on PATH). The page is plain HTML/JS (no npm build
// step): it imports the runtime the application serves at /wails/runtime.js, calls Service by name and listens for "koine:update".
//
//	cd desktop && go run .          (Linux: GTK 4 and WebKitGTK 6.0 development packages; add -tags gtk3 for the GTK 3 stack)
//	cd desktop && go build -o koine-desktop .
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"koine-desktop/bridge"
	"koine/core/rpcclient"
)

//go:embed all:frontend
var assets embed.FS

// Service is what the page can call: Wails binds every exported method, so the process wiring stays in the lifecycle hooks and the rest of
// bridge.Backend (Attach, Handle, SetEmit ...) is not reachable from the page.
type Service struct {
	b    *bridge.Backend
	proc *rpcclient.Process
}

// ServiceStartup starts the agent process; its lifetime is the application's.
func (s *Service) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	proc, err := rpcclient.Start(ctx, rpcclient.Options{Dir: s.b.Cwd})
	if err != nil {
		s.b.Fail(err) // shown in the transcript; the window still opens
		return nil
	}
	s.proc = proc
	go s.b.Attach(proc, proc.Events())
	return nil
}

// ServiceShutdown ends the agent process.
func (s *Service) ServiceShutdown() error {
	if s.proc != nil {
		return s.proc.Close()
	}
	return nil
}

// Info returns the model, directory and slash commands for the sidebar.
func (s *Service) Info() bridge.Info { return s.b.Info() }

// Send sends a prompt, or steers the run in progress.
func (s *Service) Send(text string) { s.b.Send(text) }

// Cancel interrupts the run in progress.
func (s *Service) Cancel() { s.b.Cancel() }

// Answer replies to the dialog an extension opened.
func (s *Service) Answer(value string, yes bool) { s.b.Answer(value, yes) }

// Dismiss declines the dialog.
func (s *Service) Dismiss() { s.b.Dismiss() }

func main() {
	cwd := os.Getenv("KOINE_CWD")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	svc := &Service{b: bridge.NewBackend(func(string, ...any) {}, cwd)}
	page, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	app := application.New(application.Options{
		Name:        "koine",
		Description: "Koine coding agent",
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(page)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	svc.b.SetEmit(func(name string, data ...any) { app.Event.Emit(name, data...) })
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "koine",
		Width:            1100,
		Height:           760,
		BackgroundColour: application.NewRGBA(18, 18, 20, 255),
		URL:              "/",
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
