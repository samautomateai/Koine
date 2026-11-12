package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"koine/core/agentdir"
	"koine/core/llm"
	"koine/core/pkgs"
)

// updateModels runs `koine update --models [--url URL] [--force]`: the explicit, opt-in refresh of ~/.koine/model-catalog.json. Nothing
// else in Koine fetches a catalog. The address is --url, else KOINE_MODEL_CATALOG_URL, else modelCatalogUrl in the user's settings;
// KOINE_OFFLINE (Pi: PI_OFFLINE) forbids the request. The file is changed only if the download passes validation (llm.RefreshCatalog).
func updateModels(a pkgArgs, st streams) int {
	if len(a.Rest) > 0 {
		fmt.Fprintf(st.Err, "koine: update --models takes no package sources\n")
		return 2
	}
	home, _ := os.UserHomeDir()
	u := a.URL
	if u == "" {
		u = os.Getenv("KOINE_MODEL_CATALOG_URL")
	}
	if u == "" {
		u = pkgs.ReadPrefs(home).ModelCatalogURL
	}
	if u == "" {
		fmt.Fprintf(st.Err, "koine: no model catalog address: pass --url, set KOINE_MODEL_CATALOG_URL, or set modelCatalogUrl in the user settings\n")
		return 2
	}
	dir := agentdir.Of(home)
	if dir == "" {
		fmt.Fprintf(st.Err, "koine: no agent directory (set HOME or KOINE_AGENT_DIR)\n")
		return 1
	}
	if v := os.Getenv("KOINE_OFFLINE"); v == "1" || strings.EqualFold(v, "true") {
		fmt.Fprintf(st.Err, "koine: KOINE_OFFLINE is set; not refreshing the model catalog\n")
		return 1
	}
	path := filepath.Join(dir, "model-catalog.json")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := llm.RefreshCatalog(ctx, llm.RefreshOptions{URL: u, Path: path, Force: a.Force})
	if err != nil {
		fmt.Fprintf(st.Err, "koine: model catalog: %v\n  %s was not changed\n", err, path)
		return 1
	}
	if res.NotModified {
		fmt.Fprintf(st.Out, "model catalog: up to date (%d models in %s)\n", res.Models, path)
		return 0
	}
	fmt.Fprintf(st.Out, "model catalog: %d added, %d updated, %d unchanged, %d kept from your file -> %s (%d models)\n",
		res.Added, res.Updated, res.Unchanged, res.Kept, path, res.Models)
	return 0
}
