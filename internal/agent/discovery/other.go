//go:build !linux && !windows

package discovery

import (
	"fmt"
	"runtime"
)

func Listeners() ([]Listener, error) {
	return nil, fmt.Errorf("listener discovery is experimental on %s; Linux and Windows are the supported families", runtime.GOOS)
}
