//go:build windows

package main

import (
	"context"

	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
	"golang.org/x/sys/windows/svc"
)

type windowsService struct {
	host *servicehost.Host
	ctx  context.Context
	stop context.CancelFunc
}

func (w *windowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	changes <- svc.Status{State: svc.StartPending}
	go func() { _ = w.host.Run(w.ctx) }()
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			w.stop()
			return
		}
	}
	return
}

func runWindowsService(h *servicehost.Host) bool {
	is, err := svc.IsWindowsService()
	if err != nil || !is {
		return false
	}
	ctx, stop := context.WithCancel(context.Background())
	_ = svc.Run("MonikAgent", &windowsService{host: h, ctx: ctx, stop: stop})
	return true
}
