//go:build windows

package discovery

import (
	"encoding/binary"
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	tcpTableOwnerPIDListener = 3
	afINET                   = 2
	afINET6                  = 23
	mibTCPListen             = 2
)

var (
	modiphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")
)

type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

func Listeners() ([]Listener, error) {
	v4, err := tcpTable(afINET)
	if err != nil {
		return nil, err
	}
	v6, _ := tcpTable(afINET6)
	return append(v4, v6...), nil
}

func tcpTable(family int) ([]Listener, error) {
	var size uint32
	r, _, e := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if r != 0 && windows.Errno(r) != windows.ERROR_INSUFFICIENT_BUFFER && e != windows.ERROR_INSUFFICIENT_BUFFER {
		if size == 0 {
			return nil, e
		}
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	r, _, e = procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if r != 0 {
		return nil, e
	}
	if len(buf) < 4 {
		return nil, nil
	}
	n := binary.LittleEndian.Uint32(buf[:4])
	off := 4
	var out []Listener
	rowSize := int(unsafe.Sizeof(mibTCPRowOwnerPID{}))
	if family == afINET6 {
		rowSize = 24 + 16 + 4 + 16 + 4 + 4 // state + local addr/scope/port + remote + pid, conservative
	}
	for i := uint32(0); i < n && off+rowSize <= len(buf); i++ {
		if family == afINET {
			row := (*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[off]))
			off += int(unsafe.Sizeof(mibTCPRowOwnerPID{}))
			if row.State != mibTCPListen && row.State != 2 {
				continue
			}
			ip := make(net.IP, 4)
			binary.LittleEndian.PutUint32(ip, row.LocalAddr)
			port := int(syscall.Ntohs(uint16(row.LocalPort & 0xffff)))
			out = append(out, Listener{IP: ip, Port: port, PID: int(row.OwningPID)})
			continue
		}
		// IPv6 row: DWORD state, BYTE[16] local, DWORD localScope, DWORD localPort, BYTE[16] remote, DWORD remoteScope, DWORD remotePort, DWORD pid
		if off+56 > len(buf) {
			break
		}
		state := binary.LittleEndian.Uint32(buf[off : off+4])
		lip := net.IP(append([]byte(nil), buf[off+4:off+20]...))
		lport := int(syscall.Ntohs(binary.LittleEndian.Uint16(buf[off+24 : off+26])))
		pid := int(binary.LittleEndian.Uint32(buf[off+52 : off+56]))
		off += 56
		if state != mibTCPListen && state != 2 {
			continue
		}
		out = append(out, Listener{IP: lip, Port: lport, PID: pid})
	}
	return out, nil
}
