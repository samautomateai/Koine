module koine-desktop

go 1.24

require (
	github.com/wailsapp/wails/v2 v2.13.0
	koine v0.0.0

	// Floors for transitive dependencies with known fixes (Trivy gate); go mod tidy keeps them.
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace koine => ../
