// Bubble Tea frontend for koine. It starts `koine --mode rpc` as a child process and talks to it through core/rpcclient (REQ-FE-002,
// REQ-FE-003, ADR 0009), so it needs the koine binary (KOINE_BIN, next to this program, or on PATH) and shares no code with the core
// except the standard-library client. Arguments are passed to koine, for example `koine-tui -C ~/project --model sonnet`.
//
//	cd tui-bubbletea && go mod tidy && go run .
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"koine/core/rpcclient"
)

func main() {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	proc, err := rpcclient.Start(ctx, rpcclient.Options{Args: os.Args[1:]})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	info := "starting…"
	if st, err := proc.GetState(ctx); err == nil && st.Model != nil {
		cwd, _ := os.Getwd()
		info = st.Model.ID + " · " + cwd
	}
	p := tea.NewProgram(newModel(proc, info), tea.WithAltScreen())
	go func() {
		for e := range proc.Events() {
			p.Send(evMsg(e))
		}
		p.Send(exitMsg{proc.Err()})
	}()
	_, runErr := p.Run()
	_ = proc.Close()
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
