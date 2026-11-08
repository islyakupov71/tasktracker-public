// Package tunnel manages a cloudflared quick tunnel as a child process and
// exposes the current public HTTPS URL, notifying a callback when it changes.
//
// A quick tunnel ("cloudflared tunnel --url ...") prints a line like
//
//	https://random-words-1234.trycloudflare.com
//
// to stderr. The URL changes on every (re)start, so we parse it live and let
// the bot re-publish it (e.g. via setChatMenuButton).
package tunnel

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

var urlRe = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// Tunnel runs and supervises a cloudflared quick tunnel.
type Tunnel struct {
	bin    string // path to cloudflared binary
	target string // local address to expose, e.g. http://127.0.0.1:8099

	mu    sync.RWMutex
	url   string
	onURL func(string)
}

// New creates a tunnel for the given local target. bin defaults to
// "cloudflared" if empty.
func New(bin, target string) *Tunnel {
	if bin == "" {
		bin = "cloudflared"
	}
	return &Tunnel{bin: bin, target: target}
}

// OnURL registers a callback invoked whenever the public URL changes.
func (t *Tunnel) OnURL(fn func(string)) { t.onURL = fn }

// URL returns the current public URL (empty until the tunnel is up).
func (t *Tunnel) URL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.url
}

func (t *Tunnel) setURL(u string) {
	t.mu.Lock()
	changed := u != t.url
	t.url = u
	cb := t.onURL
	t.mu.Unlock()
	if changed {
		log.Info().Str("url", u).Msg("tunnel public URL updated")
		if cb != nil {
			cb(u)
		}
	}
}

// Run supervises the cloudflared process until ctx is cancelled, restarting it
// with backoff if it exits.
func (t *Tunnel) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := t.runOnce(ctx); err != nil && ctx.Err() == nil {
			log.Warn().Err(err).Dur("retry_in", backoff).Msg("cloudflared exited, restarting")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (t *Tunnel) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, t.bin,
		"tunnel", "--url", t.target, "--no-autoupdate")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go t.scan(stdout)
	go t.scan(stderr)
	return cmd.Wait()
}

func (t *Tunnel) scan(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if m := urlRe.FindString(sc.Text()); m != "" {
			t.setURL(m)
		}
	}
}
