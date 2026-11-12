package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxCatalogBytes bounds a downloaded catalog; the built-in one is a few kilobytes.
const maxCatalogBytes = 4 << 20

// RefreshOptions describe one refresh of the user's model catalog from a server.
type RefreshOptions struct {
	URL  string // https, or http on the local machine
	Path string // the user's model-catalog.json
	HTTP *http.Client
	Now  func() time.Time
	// Force ignores the stored ETag and downloads the catalog again.
	Force bool
}

// RefreshResult says what a refresh did to the file.
type RefreshResult struct {
	Added, Updated, Unchanged int
	Kept                      int  // entries of the existing file that the server does not list
	Models                    int  // entries in the file afterwards
	NotModified               bool // the server said the stored copy is current; nothing was written
}

type rawModel struct {
	id  string
	raw json.RawMessage
}

func loopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func checkCatalogURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a catalog URL", raw)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname())) {
		return fmt.Errorf("the catalog URL must be https (http only for this machine), not %q", u.Scheme+"://"+u.Host)
	}
	return nil
}

// RefreshCatalog downloads a model catalog, validates it and merges it into the user's catalog file. It is the explicit, opt-in way to
// update model metadata (`koine update --models`); nothing in Koine fetches a catalog on its own. The file is changed only when every
// step succeeded: the download is capped, parsed and checked (ids, numbers, input kinds) before anything is written; the previous file is
// copied to <path>.bak; the new file is written beside the old one and renamed over it; and an existing file that is not valid JSON is
// left alone. Entries of the existing file keep their position and unknown fields; entries the server lists replace those with the same id,
// the others are kept. The file records the source, the time and the ETag, so a later refresh can ask whether anything changed.
func RefreshCatalog(ctx context.Context, o RefreshOptions) (RefreshResult, error) {
	var res RefreshResult
	if err := checkCatalogURL(o.URL); err != nil {
		return res, err
	}
	if o.Path == "" {
		return res, errors.New("no catalog file to write")
	}
	doc, existing, err := readCatalogDoc(o.Path)
	if err != nil {
		return res, err
	}
	var etag string
	_ = json.Unmarshal(doc["etag"], &etag)
	var source string
	_ = json.Unmarshal(doc["source"], &source)
	body, newTag, notModified, err := downloadCatalog(ctx, o, etag != "" && source == o.URL && !o.Force && existing != nil, etag)
	if err != nil {
		return res, err
	}
	if notModified {
		res.NotModified, res.Models = true, len(modelsOf(doc))
		return res, nil
	}
	remote, err := validateCatalog(body)
	if err != nil {
		return res, err
	}
	merged, res := mergeCatalogModels(modelsOf(doc), remote)
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	out := map[string]json.RawMessage{}
	for k, v := range doc {
		out[k] = v
	}
	set := func(k string, v any) { b, _ := json.Marshal(v); out[k] = b }
	set("schema", 1)
	set("source", o.URL)
	set("fetched", now().UTC().Format(time.RFC3339))
	if newTag != "" {
		set("etag", newTag)
	} else {
		delete(out, "etag")
	}
	list := make([]json.RawMessage, len(merged))
	for i, m := range merged {
		list[i] = m.raw
	}
	set("models", list)
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return res, err
	}
	data = append(data, '\n')
	if _, err = validateCatalog(data); err != nil { // what is about to be written must load again
		return res, fmt.Errorf("the merged catalog is not valid: %w", err)
	}
	return res, writeCatalog(o.Path, data, existing)
}

// readCatalogDoc reads the existing file as a JSON object. A missing file is an empty document; an unreadable or invalid one is an error
// so that it is never replaced.
func readCatalogDoc(path string) (doc map[string]json.RawMessage, existing []byte, err error) {
	existing, err = os.ReadFile(path) //nolint:gosec // the user's own catalog file
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal(existing, &doc); err != nil || doc == nil {
		return nil, nil, fmt.Errorf("%s is not valid JSON; fix or remove it first (it was not changed)", path)
	}
	return doc, existing, nil
}

func modelsOf(doc map[string]json.RawMessage) []rawModel {
	var list []json.RawMessage
	_ = json.Unmarshal(doc["models"], &list)
	out := make([]rawModel, 0, len(list))
	for _, r := range list {
		var h struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(r, &h)
		out = append(out, rawModel{h.ID, r})
	}
	return out
}

func compactJSON(b []byte) string {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return string(b)
	}
	return buf.String()
}

// mergeCatalogModels puts the server's entries into the existing list (same id: replace in place, otherwise append).
func mergeCatalogModels(existing, remote []rawModel) ([]rawModel, RefreshResult) {
	var res RefreshResult
	out := append([]rawModel(nil), existing...)
	listed := map[string]bool{}
	for _, r := range remote {
		listed[r.id] = true
		found := false
		for i := range out {
			if out[i].id != r.id {
				continue
			}
			found = true
			if compactJSON(out[i].raw) == compactJSON(r.raw) {
				res.Unchanged++
			} else {
				out[i], res.Updated = r, res.Updated+1
			}
		}
		if !found {
			out, res.Added = append(out, r), res.Added+1
		}
	}
	for _, e := range existing {
		if !listed[e.id] {
			res.Kept++
		}
	}
	res.Models = len(out)
	return out, res
}

func downloadCatalog(ctx context.Context, o RefreshOptions, conditional bool, etag string) (body []byte, newTag string, notModified bool, err error) {
	hc := &http.Client{Timeout: 30 * time.Second}
	if o.HTTP != nil {
		c := *o.HTTP
		hc = &c
	}
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if e := checkCatalogURL(req.URL.String()); e != nil {
			return fmt.Errorf("refusing redirect: %w", e)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.URL, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Accept", "application/json")
	if conditional {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("cannot reach %s: %w", o.URL, errors.Unwrap(err))
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotModified && conditional:
		return nil, "", true, nil
	case resp.StatusCode != http.StatusOK:
		return nil, "", false, fmt.Errorf("%s answered HTTP %d", o.URL, resp.StatusCode)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, "", false, err
	}
	if len(body) > maxCatalogBytes {
		return nil, "", false, fmt.Errorf("the catalog is larger than %d MiB", maxCatalogBytes>>20)
	}
	return body, resp.Header.Get("ETag"), false, nil
}

// validateCatalog parses and checks a catalog document and returns its entries untouched.
func validateCatalog(b []byte) ([]rawModel, error) {
	var doc struct {
		Schema *int              `json:"schema"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("the catalog is not valid JSON: %w", err)
	}
	if doc.Schema != nil && *doc.Schema != 1 {
		return nil, fmt.Errorf("the catalog uses schema %d; this Koine reads schema 1", *doc.Schema)
	}
	if len(doc.Models) == 0 || len(doc.Models) > 5000 {
		return nil, fmt.Errorf("the catalog lists %d models; expected 1 to 5000", len(doc.Models))
	}
	seen := map[string]bool{}
	out := make([]rawModel, 0, len(doc.Models))
	for i, r := range doc.Models {
		var m Model
		if err := json.Unmarshal(r, &m); err != nil {
			return nil, fmt.Errorf("model %d: %w", i+1, err)
		}
		if err := checkModel(m); err != nil {
			return nil, fmt.Errorf("model %d (%q): %w", i+1, m.ID, err)
		}
		if seen[m.ID] {
			return nil, fmt.Errorf("model %q is listed twice", m.ID)
		}
		seen[m.ID] = true
		out = append(out, rawModel{m.ID, r})
	}
	return out, nil
}

func checkModel(m Model) error {
	if m.ID == "" || len(m.ID) > 200 || strings.ContainsFunc(m.ID, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return errors.New("needs an id of printable characters, at most 200")
	}
	if m.ContextWindow < 0 || m.ContextWindow > 1e8 || m.MaxOutput < 0 || m.MaxOutput > 1e8 {
		return errors.New("contextWindow and maxOutput must be between 0 and 100 million")
	}
	if c := m.Cost; c != nil {
		for _, v := range []float64{c.Input, c.Output, c.CacheRead, c.CacheWrite} {
			if v < 0 || v > 1e5 {
				return errors.New("prices are dollars per million tokens, between 0 and 100000")
			}
		}
	}
	if lc := m.LongContext; lc != nil && (lc.Threshold < 0 || lc.Multiplier < 1 || lc.Multiplier > 100) {
		return errors.New("longContext needs a threshold of 0 or more and a multiplier from 1 to 100")
	}
	for _, in := range m.Input {
		if in != "text" && in != "image" {
			return fmt.Errorf("input %q is not text or image", in)
		}
	}
	return nil
}

// writeCatalog saves the new file next to the old one and renames it into place; the old content goes to <path>.bak first.
func writeCatalog(path string, data, previous []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".model-catalog-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op after the rename
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if previous != nil {
		if err = os.WriteFile(path+".bak", previous, 0o600); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), path)
}
