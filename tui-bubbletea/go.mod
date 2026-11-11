module koine-tui

go 1.24

require (
	github.com/charmbracelet/bubbles v0.21.0
	github.com/charmbracelet/bubbletea v1.3.5
	github.com/charmbracelet/lipgloss v1.1.0
	koine v0.0.0

	// Floor for a transitive dependency with a known fix (Trivy gate); go mod tidy keeps it.
	golang.org/x/text v0.41.0 // indirect
)

replace koine => ../
