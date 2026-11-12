package oauth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
	"time"
)

type callbackResult struct {
	code, state, err string
}

// callback is a one-shot loopback server for the redirect of a browser sign-in. It answers only its own path, only GET, and (when state
// is set) only with the right state, so a stray request or a page in another tab cannot end the login with a code of its own.
type callback struct {
	srv    *http.Server
	port   int
	result chan callbackResult
}

// startCallback listens on 127.0.0.1 on the first of ports that is free (0 = any port) and serves path.
func startCallback(ports []int, path, state string) (*callback, error) {
	var ln net.Listener
	var err error
	for _, p := range ports {
		if ln, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p))); err == nil {
			break
		}
	}
	if ln == nil {
		return nil, fmt.Errorf("no port for the sign-in callback: %w", err)
	}
	cb := &callback{port: ln.Addr().(*net.TCPAddr).Port, result: make(chan callbackResult, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || (state != "" && q.Get("state") != state) {
			page(w, http.StatusBadRequest, "This sign-in link does not match the one Koine started. Close this tab and try again.")
			return
		}
		res := callbackResult{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}
		if res.err != "" && q.Get("error_description") != "" {
			res.err += ": " + q.Get("error_description")
		}
		if res.code == "" && res.err == "" {
			page(w, http.StatusBadRequest, "The sign-in answer carried no code.")
			return
		}
		if res.err != "" {
			page(w, http.StatusOK, "Sign-in failed: "+res.err)
		} else {
			page(w, http.StatusOK, "Signed in. You can close this tab and return to Koine.")
		}
		select {
		case cb.result <- res:
		default:
		}
	})
	cb.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = cb.srv.Serve(ln) }()
	return cb, nil
}

func page(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>Koine</title><p>%s", html.EscapeString(msg))
}

func (c *callback) close() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.srv.Shutdown(ctx)
}

// portsFor lists the ports to try: the preferred one (so it can be forwarded into a container or over SSH), then any free one.
func portsFor(preferred int) []int {
	if preferred == 0 {
		return []int{0}
	}
	return []int{preferred, 0}
}

// await waits for the code from the callback server or, when ui.Race allows, from a pasted line, whichever comes first. With no server
// the pasted line is the only way, so a missing prompt is an error. state is checked on a pasted URL when it carries one.
func await(ctx context.Context, ui UI, cb *callback, question, state string) (code, usedState string, err error) {
	ctx, cancel := context.WithTimeout(ctx, ui.timeout())
	defer cancel()
	type pasted struct {
		code, state string
		err         error
	}
	paste := make(chan pasted, 1)
	if ui.Prompt != nil && (cb == nil || ui.Race) {
		go func() {
			line, e := ui.Prompt(question, false)
			c, s := parseInput(line)
			paste <- pasted{c, s, e}
		}()
	} else if cb == nil {
		return "", "", errors.New("the sign-in callback could not be started and there is nowhere to paste the code; use --no-browser")
	}
	var results chan callbackResult
	if cb != nil {
		results = cb.result
	}
	for {
		select {
		case r := <-results:
			if r.err != "" {
				return "", "", fmt.Errorf("the sign-in was refused: %s", r.err)
			}
			return r.code, r.state, nil
		case p := <-paste:
			var bad error
			switch {
			case p.err != nil:
				bad = p.err
			case p.code == "":
				bad = errors.New("no authorization code was found in what was pasted")
			case p.state != "" && state != "" && p.state != state:
				bad = errors.New("the pasted sign-in answer belongs to another login (state mismatch)")
			}
			if bad == nil {
				return p.code, p.state, nil
			}
			if cb == nil {
				return "", "", bad
			}
			paste = nil // the browser may still answer; a failed or empty paste (stdin at EOF) must not end the wait
		case <-ctx.Done():
			return "", "", errors.New("the sign-in did not finish in time")
		}
	}
}
