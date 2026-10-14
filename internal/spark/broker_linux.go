//go:build linux

package spark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"golang.org/x/sys/unix"
)

type brokerRequest struct {
	Kind              string                  `json:"kind"`
	AgentID           string                  `json:"agent_id,omitempty"`
	ControllerID      string                  `json:"controller_id,omitempty"`
	Session           string                  `json:"session,omitempty"`
	Action            *ActionRequest          `json:"action,omitempty"`
	Log               *protocol.SparkLogLease `json:"log,omitempty"`
	PriorID           string                  `json:"prior_id,omitempty"`
	ProfileID         string                  `json:"profile_id,omitempty"`
	PriorAction       string                  `json:"prior_action,omitempty"`
	ConfirmDisruption bool                    `json:"confirm_disruption,omitempty"`
}
type brokerResponse struct {
	Error    string                   `json:"error,omitempty"`
	Snapshot *BrokerSnapshot          `json:"snapshot,omitempty"`
	Receipt  *protocol.JobReceipt     `json:"receipt,omitempty"`
	Record   *protocol.SparkLogRecord `json:"record,omitempty"`
}
type localLogLease struct {
	lease protocol.SparkLogLease
	until time.Time
}

func (c *Coordinator) renewLogLease(lease protocol.SparkLogLease) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.profile(lease.Request.ProfileID)
	rp := c.journal.Profiles[lease.Request.ProfileID]
	if !ok || !c.Policy.Enabled || !p.LogEnabled || lease.Request.ProfileRevision != p.Revision || lease.Request.ControlEpoch != c.journal.Epoch || lease.Request.ExpectedAgentSession != c.session || lease.Request.ExpectedGeneration != rp.Generation || !protocol.SparkValidID(lease.StreamID) {
		return fmt.Errorf("log_binding_invalid")
	}
	remaining := time.Until(lease.Deadline)
	if remaining <= 0 || remaining > 35*time.Second {
		return fmt.Errorf("log_lease_expired")
	}
	for id, old := range c.logLeases {
		if time.Now().After(old.until) {
			delete(c.logLeases, id)
		}
	}
	if old, exists := c.logLeases[lease.StreamID]; exists && digest(old.lease.Request) != digest(lease.Request) {
		return fmt.Errorf("log_binding_changed")
	}
	if len(c.logLeases) >= 16 {
		if _, exists := c.logLeases[lease.StreamID]; !exists {
			return fmt.Errorf("log_viewer_limit")
		}
	}
	c.logLeases[lease.StreamID] = localLogLease{lease: lease, until: time.Now().Add(remaining)}
	return nil
}

func peerCredentials(conn *net.UnixConn) (*unix.Ucred, error) {
	raw, e := conn.SyscallConn()
	if e != nil {
		return nil, e
	}
	var cred *unix.Ucred
	var err error
	e = raw.Control(func(fd uintptr) { cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if e != nil {
		return nil, e
	}
	return cred, err
}

// ServeBroker is a local root-owned, peer-credential authenticated typed service.
// It cannot execute a shell, generic Docker request, arbitrary unit or path.
func ServeBroker(ctx context.Context, policyPath, socketPath string) (serveErr error) {
	if os.Geteuid() != 0 {
		return fmt.Errorf("production broker requires local root administration")
	}
	p, e := LoadPolicy(policyPath, 0)
	if e != nil {
		return e
	}
	if e = trustedPath(p.StateDir, 0, true); e != nil {
		return e
	}
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath || filepath.Dir(socketPath) == "/tmp" {
		return fmt.Errorf("protected Unix socket directory required")
	}
	if e = trustedPath(filepath.Dir(socketPath), 0, true); e != nil {
		return e
	}
	unlock, e := processlock.Acquire(filepath.Join(p.StateDir, "broker.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	l, closeSocket, e := listenBrokerSocket(socketPath, 0, p.SocketGID)
	if e != nil {
		return e
	}
	defer func() { serveErr = errors.Join(serveErr, closeSocket()) }()
	c, e := OpenCoordinator(p, nil)
	if e != nil {
		return e
	}
	defer c.logs.CloseAll("broker_stopped")
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-watchDone:
		}
	}()
	slots := make(chan struct{}, 24)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, e := l.AcceptUnix()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			defer conn.Close()
			c.handleBrokerConnection(ctx, conn)
		}()
	}
}

// ServeBroker validates the protected directory and root authority before this
// helper runs. The socket lock is independent of StateDir and keeps its inode,
// just like the existing broker state lock.
func listenBrokerSocket(socketPath string, owner, gid uint32) (*net.UnixListener, func() error, error) {
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath || owner != uint32(os.Geteuid()) {
		return nil, nil, fmt.Errorf("invalid broker socket authority or path")
	}
	lockPath := socketPath + ".lock"
	before, err := os.Lstat(lockPath)
	if err == nil {
		if err = brokerSocketLockInfo(before, owner); err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	unlock, err := processlock.Acquire(lockPath)
	if err != nil {
		return nil, nil, err
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			unlock()
		}
	}()
	after, err := os.Lstat(lockPath)
	if err != nil {
		return nil, nil, err
	}
	if err = brokerSocketLockInfo(after, owner); err != nil {
		return nil, nil, err
	}
	if before != nil && !os.SameFile(before, after) {
		return nil, nil, fmt.Errorf("broker socket lock inode changed")
	}
	if st, err := os.Lstat(socketPath); err == nil {
		if err = brokerSocketInfo(st, owner); err != nil {
			return nil, nil, err
		}
		conn, probeErr := net.DialTimeout("unix", socketPath, 250*time.Millisecond)
		if probeErr == nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("broker socket is active")
		}
		// Only ECONNREFUSED proves a dead stream socket. Permission errors,
		// timeouts, different socket types and path changes remain occupied.
		if !errors.Is(probeErr, unix.ECONNREFUSED) {
			return nil, nil, fmt.Errorf("broker socket occupancy is uncertain: %w", probeErr)
		}
		removed, err := removeBrokerSocketIfSame(socketPath, st, owner)
		if err != nil {
			return nil, nil, err
		}
		if !removed {
			return nil, nil, fmt.Errorf("stale broker socket inode changed")
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, nil, err
	}
	// Close must never unlink a later listener or file installed at this path.
	l.SetUnlinkOnClose(false)
	owned, err := os.Lstat(socketPath)
	if err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	if err = brokerSocketIdentityInfo(owned, owner); err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	var once sync.Once
	var closeErr error
	cleanup := func() error {
		once.Do(func() {
			closeErr = l.Close()
			if errors.Is(closeErr, net.ErrClosed) {
				closeErr = nil
			}
			_, removeErr := removeBrokerSocketIfSame(socketPath, owned, owner)
			closeErr = errors.Join(closeErr, removeErr)
			unlock()
		})
		return closeErr
	}
	releaseOnError = false
	if err = os.Chown(socketPath, int(owner), int(gid)); err != nil {
		_ = cleanup()
		return nil, nil, err
	}
	if err = os.Chmod(socketPath, 0660); err != nil {
		_ = cleanup()
		return nil, nil, err
	}
	return l, cleanup, nil
}

func brokerSocketLockInfo(st os.FileInfo, owner uint32) error {
	native, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || native.Uid != owner || native.Nlink != 1 {
		return fmt.Errorf("broker socket lock must be an owner-private regular inode")
	}
	return nil
}

func brokerSocketInfo(st os.FileInfo, owner uint32) error {
	if err := brokerSocketIdentityInfo(st, owner); err != nil {
		return err
	}
	if st.Mode().Perm()&0002 != 0 {
		return fmt.Errorf("broker socket path has unsafe permissions")
	}
	return nil
}

func brokerSocketIdentityInfo(st os.FileInfo, owner uint32) error {
	native, ok := st.Sys().(*syscall.Stat_t)
	if !ok || st.Mode()&os.ModeSocket == 0 || native.Uid != owner || native.Nlink != 1 {
		return fmt.Errorf("broker socket path has unsafe type or owner")
	}
	return nil
}

func removeBrokerSocketIfSame(path string, expected os.FileInfo, owner uint32) (bool, error) {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !os.SameFile(expected, current) {
		return false, nil
	}
	if err = brokerSocketIdentityInfo(current, owner); err != nil {
		return false, err
	}
	if err = os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
func (c *Coordinator) handleBrokerConnection(ctx context.Context, conn *net.UnixConn) {
	cred, e := peerCredentials(conn)
	if e != nil || cred == nil || cred.Uid != c.Policy.CollectorUID && cred.Uid != 0 {
		return
	}
	if cred.Uid != 0 {
		if e = collectorIdentityAllowed(cred.Uid, int(cred.Pid)); e != nil {
			return
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var req brokerRequest
	// Decode one bounded request; encoder appends newline. Duplicate/unknown
	// fields are rejected, and no client-supplied URL/command reaches an adapter.
	dec := json.NewDecoder(&limitedReader{Reader: conn, remaining: 64 << 10})
	var raw json.RawMessage
	if e = dec.Decode(&raw); e != nil {
		return
	}
	if e = strictDecode(raw, &req); e != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	enc := json.NewEncoder(conn)
	safeError := func(code string) {
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_ = enc.Encode(brokerResponse{Error: code})
	}
	switch req.Kind {
	case "bind":
		if cred.Uid != c.Policy.CollectorUID {
			safeError("collector_identity_required")
			return
		}
		e = c.Bind(req.AgentID, req.ControllerID, req.Session)
		if e != nil {
			safeError("identity_mismatch")
			return
		}
		_ = enc.Encode(brokerResponse{})
	case "snapshot":
		probe, end := context.WithTimeout(ctx, 8*time.Second)
		defer end()
		snapshot, e := c.Snapshot(probe)
		if e != nil {
			safeError("snapshot_journal_unavailable")
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = enc.Encode(brokerResponse{Snapshot: &snapshot})
	case "action":
		if req.Action == nil {
			safeError("typed_action_required")
			return
		}
		if req.Action.Local && cred.Uid != 0 {
			safeError("local_root_authority_required")
			return
		}
		effectCtx, end := context.WithTimeout(ctx, time.Hour)
		defer end()
		receipt := c.Execute(effectCtx, *req.Action)
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = enc.Encode(brokerResponse{Receipt: &receipt})
	case "local_action":
		if cred.Uid != 0 || !protocol.SparkValidID(req.PriorID) || !protocol.SparkValidID(req.ProfileID) || !member(req.PriorAction, "start", "stop", "restart") {
			safeError("typed_local_root_action_required")
			return
		}
		receipt := c.ExecuteLocal(ctx, req.PriorID, req.ProfileID, req.PriorAction, req.ConfirmDisruption)
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = enc.Encode(brokerResponse{Receipt: &receipt})
	case "prior_action":
		if !protocol.SparkValidID(req.PriorID) || !protocol.SparkValidID(req.ProfileID) || !member(req.PriorAction, "start", "stop", "restart") {
			safeError("invalid_prior_action")
			return
		}
		receipt, e := c.PreviousAction(ctx, req.PriorID, req.ProfileID, req.PriorAction)
		if e != nil {
			safeError("prior_action_binding_conflict")
			return
		}
		_ = enc.Encode(brokerResponse{Receipt: receipt})
	case "renew_logs":
		if req.Log == nil || c.renewLogLease(*req.Log) != nil {
			safeError("log_binding_or_lease_invalid")
			return
		}
		_ = enc.Encode(brokerResponse{})
	case "logs":
		if req.Log == nil {
			safeError("typed_log_lease_required")
			return
		}
		lease := *req.Log
		c.mu.Lock()
		p, ok := c.profile(lease.Request.ProfileID)
		rp := c.journal.Profiles[lease.Request.ProfileID]
		valid := ok && c.Policy.Enabled && p.LogEnabled && lease.Request.ProfileRevision == p.Revision && lease.Request.ControlEpoch == c.journal.Epoch && lease.Request.ExpectedAgentSession == c.session && lease.Request.ExpectedGeneration == rp.Generation
		c.mu.Unlock()
		if !valid || !protocol.SparkValidID(lease.StreamID) || !time.Now().Before(lease.Deadline) || time.Until(lease.Deadline) > 35*time.Second {
			safeError("log_binding_or_lease_invalid")
			return
		}
		if e = c.renewLogLease(lease); e != nil {
			safeError("log_binding_or_lease_invalid")
			return
		}
		streamCtx, end := context.WithCancel(ctx)
		defer end()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		sub, e := c.logs.Subscribe(streamCtx, p, c.supervisor)
		if e != nil {
			safeError("log_limit_or_source_unavailable")
			return
		}
		defer sub.Close()
		write := func(record protocol.SparkLogRecord) bool {
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			return enc.Encode(brokerResponse{Record: &record}) == nil
		}
		for _, r := range sub.Tail {
			if !write(r) {
				return
			}
		}
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-timer.C:
				c.mu.Lock()
				current, exists := c.logLeases[lease.StreamID]
				still := exists && time.Now().Before(current.until) && digest(current.lease.Request) == digest(lease.Request) && c.session == lease.Request.ExpectedAgentSession && c.journal.Profiles[p.ID].Generation == lease.Request.ExpectedGeneration
				c.mu.Unlock()
				if !still {
					return
				}
			case record, open := <-sub.Records:
				if !open {
					return
				}
				c.mu.Lock()
				current := c.journal.Profiles[p.ID]
				still := c.Policy.Enabled && c.session == lease.Request.ExpectedAgentSession && c.journal.Epoch == lease.Request.ControlEpoch && current.Generation == lease.Request.ExpectedGeneration
				c.mu.Unlock()
				if !still {
					safeError("log_authority_changed")
					return
				}
				if !write(record) {
					return
				}
			}
		}
	default:
		safeError("unknown_broker_request")
	}
}

type limitedReader struct {
	Reader    interface{ Read([]byte) (int, error) }
	remaining int
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, fmt.Errorf("request_limit")
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, e := r.Reader.Read(p)
	r.remaining -= n
	return n, e
}

type BrokerClient struct{ Socket string }

func (b BrokerClient) connect(ctx context.Context) (*net.UnixConn, error) {
	d := net.Dialer{Timeout: time.Second}
	conn, e := d.DialContext(ctx, "unix", b.Socket)
	if e != nil {
		return nil, fmt.Errorf("local_broker_unavailable")
	}
	u := conn.(*net.UnixConn)
	cred, e := peerCredentials(u)
	if e != nil || cred.Uid != 0 {
		u.Close()
		return nil, fmt.Errorf("broker_root_identity_required")
	}
	return u, nil
}
func (b BrokerClient) call(ctx context.Context, request brokerRequest) (brokerResponse, error) {
	var out brokerResponse
	conn, e := b.connect(ctx)
	if e != nil {
		return out, e
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if e = json.NewEncoder(conn).Encode(request); e != nil {
		return out, e
	}
	var raw json.RawMessage
	if e = json.NewDecoder(&limitedReader{Reader: conn, remaining: 2 << 20}).Decode(&raw); e != nil {
		return out, e
	}
	if e = strictDecode(raw, &out); e != nil {
		return out, e
	}
	if out.Error != "" {
		return out, fmt.Errorf("%s", out.Error)
	}
	return out, nil
}
func (b BrokerClient) Bind(ctx context.Context, agent, controller, session string) error {
	_, e := b.call(ctx, brokerRequest{Kind: "bind", AgentID: agent, ControllerID: controller, Session: session})
	return e
}
func (b BrokerClient) Snapshot(ctx context.Context) (BrokerSnapshot, error) {
	r, e := b.call(ctx, brokerRequest{Kind: "snapshot"})
	if e != nil {
		return BrokerSnapshot{}, e
	}
	if r.Snapshot == nil {
		return BrokerSnapshot{}, fmt.Errorf("missing broker snapshot")
	}
	return *r.Snapshot, nil
}
func (b BrokerClient) Execute(ctx context.Context, r ActionRequest) (protocol.JobReceipt, error) {
	v, e := b.call(ctx, brokerRequest{Kind: "action", Action: &r})
	if e != nil {
		return protocol.JobReceipt{}, e
	}
	if v.Receipt == nil {
		return protocol.JobReceipt{}, fmt.Errorf("missing durable receipt")
	}
	return *v.Receipt, nil
}
func (b BrokerClient) PreviousAction(ctx context.Context, id, profile, action string) (*protocol.JobReceipt, error) {
	v, e := b.call(ctx, brokerRequest{Kind: "prior_action", PriorID: id, ProfileID: profile, PriorAction: action})
	return v.Receipt, e
}
func (b BrokerClient) LocalAction(ctx context.Context, id, profile, action string, confirm bool) (protocol.JobReceipt, error) {
	v, e := b.call(ctx, brokerRequest{Kind: "local_action", PriorID: id, ProfileID: profile, PriorAction: action, ConfirmDisruption: confirm})
	if e != nil {
		return protocol.JobReceipt{}, e
	}
	if v.Receipt == nil {
		return protocol.JobReceipt{}, fmt.Errorf("missing local action receipt")
	}
	return *v.Receipt, nil
}
func (b BrokerClient) RenewLogs(ctx context.Context, lease protocol.SparkLogLease) error {
	_, e := b.call(ctx, brokerRequest{Kind: "renew_logs", Log: &lease})
	return e
}
func (b BrokerClient) Logs(ctx context.Context, lease protocol.SparkLogLease, emit func(protocol.SparkLogRecord) error) error {
	conn, e := b.connect(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	if e = json.NewEncoder(conn).Encode(brokerRequest{Kind: "logs", Log: &lease}); e != nil {
		return e
	}
	go func() { <-ctx.Done(); conn.Close() }()
	d := json.NewDecoder(conn)
	for {
		var r brokerResponse
		if e = d.Decode(&r); e != nil {
			return e
		}
		if r.Error != "" {
			return fmt.Errorf("%s", r.Error)
		}
		if r.Record != nil {
			if e = emit(*r.Record); e != nil {
				return e
			}
		}
	}
}
