package discovery

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
)

const tcpFamily4 = 2
const tcpFamily6 = 23

// Decode the documented MIB_TCP(6)TABLE_OWNER_PID wire layout without unsafe
// struct casts. IPv6 starts with the LOCAL ADDRESS, not the state DWORD.
// Source: Microsoft tcpmib.h MIB_TCP6ROW_OWNER_PID and MIB_TCPROW_OWNER_PID.
func parseOwnerPIDTable(buf []byte, family int) ([]Listener, error) {
	stride := 24
	if family == tcpFamily6 {
		stride = 56
	} else if family != tcpFamily4 {
		return nil, fmt.Errorf("unsupported TCP address family")
	}
	if len(buf) < 4 {
		return nil, fmt.Errorf("truncated TCP table header")
	}
	count := uint64(binary.LittleEndian.Uint32(buf[:4]))
	if count > uint64((len(buf)-4)/stride) {
		return nil, fmt.Errorf("truncated TCP table rows")
	}
	out := make([]Listener, 0, int(count))
	for i := uint64(0); i < count; i++ {
		row := buf[4+int(i)*stride : 4+(int(i)+1)*stride]
		stateOffset, portOffset, pidOffset := 0, 8, 20
		ip := net.IP(append([]byte(nil), row[4:8]...))
		zone := ""
		if family == tcpFamily6 {
			stateOffset, portOffset, pidOffset = 48, 20, 52
			ip = net.IP(append([]byte(nil), row[:16]...))
			// Microsoft documents scope IDs, unlike state/PID, in network byte order.
			scope := binary.BigEndian.Uint32(row[16:20])
			if scope != 0 && ip.IsLinkLocalUnicast() {
				zone = strconv.FormatUint(uint64(scope), 10)
			}
		}
		if binary.LittleEndian.Uint32(row[stateOffset:stateOffset+4]) != 2 {
			continue
		}
		port := int(binary.BigEndian.Uint16(row[portOffset : portOffset+2]))
		if port == 0 {
			return nil, fmt.Errorf("invalid listening port in TCP table")
		}
		out = append(out, Listener{IP: ip, Port: port, PID: int(binary.LittleEndian.Uint32(row[pidOffset : pidOffset+4])), Zone: zone})
	}
	return out, nil
}
