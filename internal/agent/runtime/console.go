package runtime

import (
	"context"
	"net/http"
	"time"

	ac "github.com/alinescafs3mp-afk/monik_v2/internal/agentconsole"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

func (a *Agent) consoleBinding() ac.Binding {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := ac.Binding{URL: a.State.File.ControllerURL, AgentID: a.State.File.AgentID, Credential: a.Cred, WorkerSession: a.Session, ControllerID: a.State.File.ControllerID, TrustIdentity: secure.SHA256Bytes([]byte(a.State.File.CACertPEM))}
	if t, ok := a.client.Transport.(*http.Transport); ok && t.TLSClientConfig != nil {
		b.TLS = t.TLSClientConfig.Clone()
	}
	return b
}
func (a *Agent) consoleLoop(ctx context.Context) {
	delay := 2 * time.Second
	for ctx.Err() == nil {
		if ac.LocalAvailable() {
			name, e := ac.LocalUser(ctx, ac.DialLocal)
			if e == nil {
				b := a.consoleBinding()
				sock, e := ac.Dial(ctx, b)
				if e == nil {
					linkctx, cancel := context.WithCancel(ctx)
					done := make(chan struct{})
					go func() {
						defer close(done)
						t := time.NewTicker(time.Second)
						defer t.Stop()
						for {
							select {
							case <-linkctx.Done():
								return
							case <-t.C:
								now := a.consoleBinding()
								if now.URL != b.URL || now.Credential != b.Credential || now.ControllerID != b.ControllerID || now.TrustIdentity != b.TrustIdentity || !ac.LocalAvailable() {
									cancel()
									return
								}
							}
						}
					}()
					_ = ac.RunLink(linkctx, sock, b.ControllerID, name, ac.DialLocal)
					cancel()
					<-done
					delay = 2 * time.Second
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
}
func (a *Agent) startConsole(ctx context.Context) {
	a.workerTasks.Add(1)
	go func() { defer a.workerTasks.Done(); a.consoleLoop(ctx) }()
}
