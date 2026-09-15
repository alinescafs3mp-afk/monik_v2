package discovery

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
)

func TestV14SocketInventoryIsIndependentOfHTTPBudget(t *testing.T) {
	ls := []Listener{{IP: net.ParseIP("0.0.0.0"), Port: 9001}, {IP: net.ParseIP("::"), Port: 9001}}
	d := InspectListenersPolicy(context.Background(), ls, nil, 0, nil, nil, false)
	if d.InventoryVersion != 1 || !d.ListenerCoverageComplete || len(d.ListenerTargets) != 2 || d.CoverageComplete || len(d.Confirmed) != 0 {
		t.Fatalf("%+v", d)
	}
}
func TestV14SocketInventoryLimitIsNotComplete(t *testing.T) {
	ls := make([]Listener, 4097)
	for i := range ls {
		ls[i] = Listener{IP: net.ParseIP("127.0.0.1"), Port: 1000 + i}
	}
	d := InspectListenersPolicy(context.Background(), ls, nil, 0, nil, nil, false)
	if d.ListenerCoverageComplete || len(d.ListenerTargets) != 4096 {
		t.Fatal(d.ListenerCoverageComplete, len(d.ListenerTargets))
	}
}
func TestV14WindowsIPv6TableLayoutAndScope(t *testing.T) {
	b := make([]byte, 4+112)
	binary.LittleEndian.PutUint32(b, 2)
	r := b[4:60]
	copy(r, net.ParseIP("::1").To16())
	binary.BigEndian.PutUint16(r[20:], 8777)
	binary.LittleEndian.PutUint32(r[48:], 2)
	binary.LittleEndian.PutUint32(r[52:], 123)
	r = b[60:]
	copy(r, net.ParseIP("fe80::123").To16())
	binary.BigEndian.PutUint32(r[16:], 7)
	binary.BigEndian.PutUint16(r[20:], 8080)
	binary.LittleEndian.PutUint32(r[48:], 2)
	binary.LittleEndian.PutUint32(r[52:], 456)
	rows, e := parseOwnerPIDTable(b, tcpFamily6)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	if rows[0].IP.String() != "::1" || rows[0].Port != 8777 || rows[0].PID != 123 || rows[1].Zone != "7" {
		t.Fatal(rows)
	}
	targets := DialTargets(rows, nil)
	// Link-local destinations remain excluded by the existing probe policy.
	if len(targets) != 1 || targets[0] != "[::1]:8777" {
		t.Fatal(targets)
	}
}
func TestV14WindowsIPv4TableAndMalformedLengths(t *testing.T) {
	b := make([]byte, 28)
	binary.LittleEndian.PutUint32(b, 1)
	binary.LittleEndian.PutUint32(b[4:], 2)
	copy(b[8:12], []byte{127, 0, 0, 1})
	binary.BigEndian.PutUint16(b[12:], 80)
	binary.LittleEndian.PutUint32(b[24:], 12)
	r, e := parseOwnerPIDTable(b, tcpFamily4)
	if e != nil || len(r) != 1 || r[0].Port != 80 || r[0].PID != 12 || r[0].IP.String() != "127.0.0.1" {
		t.Fatal(r, e)
	}
	for _, n := range []int{0, 1, 3, 4, 20, 27} {
		if _, e = parseOwnerPIDTable(b[:n], tcpFamily4); e == nil {
			t.Fatal("truncated table accepted", n)
		}
	}
	binary.LittleEndian.PutUint32(b, 0xffffffff)
	if _, e = parseOwnerPIDTable(b, tcpFamily4); e == nil {
		t.Fatal("overflow count accepted")
	}
}
func FuzzV14OwnerPIDTable(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0}, true)
	f.Add(make([]byte, 60), false)
	f.Fuzz(func(t *testing.T, b []byte, v6 bool) {
		if len(b) > 1<<20 {
			return
		}
		family := tcpFamily4
		if v6 {
			family = tcpFamily6
		}
		rows, e := parseOwnerPIDTable(b, family)
		if e == nil {
			for _, r := range rows {
				if r.Port < 1 || r.Port > 65535 || r.IP == nil {
					t.Fatal("invalid successful parser output")
				}
			}
		}
	})
}
