//go:build windows

package discovery

import (
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

const tcpTableOwnerPIDListener = 3

var (
	modiphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")
)

func Listeners() ([]Listener, error) {
	v4, err := tcpTable(tcpFamily4)
	if err != nil {
		return nil, err
	}
	v6, err := tcpTable(tcpFamily6)
	if err != nil {
		return nil, fmt.Errorf("IPv6 listener inventory incomplete: %w", err)
	}
	return append(v4, v6...), nil
}
func tcpTable(family int) ([]Listener, error) {
	var size uint32
	r, _, _ := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if r != 0 && windows.Errno(r) != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, windows.Errno(r)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if size < 4 || size > 8<<20 {
			return nil, fmt.Errorf("invalid TCP table size")
		}
		buf := make([]byte, size)
		r, _, _ = procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
		if windows.Errno(r) == windows.ERROR_INSUFFICIENT_BUFFER {
			continue
		}
		if r != 0 {
			return nil, windows.Errno(r)
		}
		if int(size) > len(buf) {
			return nil, fmt.Errorf("TCP table grew without size error")
		}
		return parseOwnerPIDTable(buf[:size], family)
	}
	return nil, fmt.Errorf("TCP table changed repeatedly; retry the next inventory scan")
}
