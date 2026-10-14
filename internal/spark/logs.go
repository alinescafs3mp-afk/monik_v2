package spark

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

const (
	LogMaxLine      = 16 << 10
	LogProfileBytes = 256 << 10
	LogHostBytes    = 1 << 20
	LogMaxProfiles  = 4
	LogMaxViewers   = 16
)

type LogReader interface {
	io.Reader
	io.Closer
}
type commandLogReader struct {
	io.ReadCloser
	cmd  *exec.Cmd
	once sync.Once
}

func (r *commandLogReader) Close() error {
	r.once.Do(func() {
		if r.cmd.Process != nil {
			_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
		}
		r.ReadCloser.Close()
		_ = r.cmd.Wait()
	})
	return nil
}
func (s LocalSupervisor) Logs(ctx context.Context, p Profile) (LogReader, error) {
	if p.Target.Kind == existingCoordinatorKind {
		return existingLogs(ctx, p)
	}
	if p.ObservationOnly || p.Target.Kind == configuredCatalogKind || !p.LogEnabled {
		return nil, fmt.Errorf("logs_not_locally_approved")
	}
	if p.Target.Kind == "docker" {
		return dockerLogs(ctx, p)
	}
	probe, end := context.WithTimeout(ctx, 2*time.Second)
	hash, e := (LocalSupervisor{}).Definition(probe, p)
	end()
	if e != nil || hash != p.Target.DefinitionHash {
		return nil, fmt.Errorf("approved log unit definition drift")
	}
	if e := trustedPath("/usr/bin/journalctl", 0, false); e != nil {
		return nil, e
	}
	args := []string{"--no-pager", "--output=cat", "--follow", "--lines=200", "--unit=" + p.Target.Unit}
	if p.Target.Kind == "systemd-user" {
		args = append([]string{"--user"}, args...)
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/journalctl", args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if uid := systemdUID(p); uid != 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", uid))
		if os.Geteuid() == 0 {
			cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: uid, Groups: []uint32{}}
		}
	}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	reader, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		return nil, fmt.Errorf("approved journal source unavailable")
	}
	return &commandLogReader{ReadCloser: reader, cmd: cmd}, nil
}

var ansi = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)
var credentialPattern = regexp.MustCompile(`(?i)(["']?(?:authorization|api[_-]?key|access[_-]?token|password|secret|hf_token)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*(?:"|\\?$)|'(?:\\.|[^'\\])*(?:'|\\?$)|(?:bearer[ \t]+)?[^\s,;}\]]+)`)
var tokenPattern = regexp.MustCompile(`(?:hf_[A-Za-z0-9]{12,}|sk-[A-Za-z0-9_-]{12,})`)

type Redactor struct {
	secrets []string
	// Existing file sources bind this to the same protected descriptor whose
	// bytes were hash-verified. Check again at the retention/transport boundary.
	existingGuard func() error
}

func newRedactor(p Profile) (*Redactor, error) {
	if p.Target.Kind == existingCoordinatorKind {
		return existingLogRedactor(p)
	}
	r := &Redactor{}
	totalSecrets := 0
	for _, path := range p.RedactionFiles {
		if e := trustedPath(path, 0, false); e != nil {
			return nil, fmt.Errorf("redaction_source_unavailable")
		}
		b, e := readBoundedFile(path, 64<<10)
		if e != nil {
			return nil, fmt.Errorf("redaction_source_unavailable")
		}
		for _, secret := range strings.Split(string(b), "\n") {
			secret = strings.TrimSpace(secret)
			if len(secret) > 0 {
				if len(secret) > LogMaxLine {
					return nil, fmt.Errorf("redaction_secret_too_large")
				}
				r.secrets = append(r.secrets, secret)
				totalSecrets += len(secret)
				if len(r.secrets) > 512 || totalSecrets > 256<<10 {
					return nil, fmt.Errorf("redaction_source_budget_exceeded")
				}
			}
		}
	}
	return r, nil
}
func (r *Redactor) Clean(line []byte) string {
	if len(line) > LogMaxLine {
		return "[oversized log record dropped]"
	}
	s := strings.ToValidUTF8(string(line), "�")
	s = ansi.ReplaceAllString(s, "")
	s = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\t' {
			return -1
		}
		return c
	}, s)
	if len(s) > LogMaxLine {
		return "[oversized normalized record dropped]"
	}
	pairs := []string{}
	for _, secret := range r.secrets {
		if secret != "" {
			pairs = append(pairs, secret, "[redacted]")
		}
	}
	if len(pairs) > 0 {
		s = strings.NewReplacer(pairs...).Replace(s)
	}
	if len(s) > LogMaxLine {
		return "[oversized sanitized record dropped]"
	}
	s = credentialPattern.ReplaceAllString(s, "${1}[redacted]")
	s = tokenPattern.ReplaceAllString(s, "[redacted]")
	if len(s) > LogMaxLine {
		return "[oversized sanitized record dropped]"
	}
	s = ansi.ReplaceAllString(s, "")
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\t' {
			return -1
		}
		return c
	}, s)
}

// Complete-line assembly ensures a secret split across reads is filtered before
// retention or transmission. Oversized/incomplete lines are never partially sent.
type RecordAssembler struct {
	pending  []byte
	dropping bool
}

func (a *RecordAssembler) Feed(chunk []byte, emit func([]byte, bool)) {
	for len(chunk) > 0 {
		idx := bytes.IndexByte(chunk, '\n')
		part := chunk
		if idx >= 0 {
			part = chunk[:idx]
		}
		if !a.dropping {
			if len(a.pending)+len(part) > LogMaxLine {
				a.pending = nil
				a.dropping = true
			} else {
				a.pending = append(a.pending, part...)
			}
		}
		if idx < 0 {
			return
		}
		if a.dropping {
			emit([]byte("[oversized log record dropped]"), true)
		} else {
			emit(a.pending, false)
		}
		a.pending = nil
		a.dropping = false
		chunk = chunk[idx+1:]
	}
}
func (a *RecordAssembler) Finish(emit func([]byte, bool)) {
	if len(a.pending) > 0 || a.dropping {
		emit([]byte("[incomplete log record dropped]"), true)
	}
	a.pending = nil
	a.dropping = false
}

type logProfile struct {
	records []protocol.SparkLogRecord
	used    int
	seq     int64
	viewers map[int]chan protocol.SparkLogRecord
	cancel  context.CancelFunc
	closing bool
}
type LogHub struct {
	mu       sync.Mutex
	profiles map[string]*logProfile
	viewers  int
	next     int
}

func NewLogHub() *LogHub { return &LogHub{profiles: map[string]*logProfile{}} }

type LogSubscription struct {
	Records <-chan protocol.SparkLogRecord
	Tail    []protocol.SparkLogRecord
	Close   func()
}

func (h *LogHub) Subscribe(ctx context.Context, p Profile, s Supervisor) (LogSubscription, error) {
	h.mu.Lock()
	if h.viewers >= LogMaxViewers {
		h.mu.Unlock()
		return LogSubscription{}, fmt.Errorf("log_viewer_limit")
	}
	lp := h.profiles[p.ID]
	if lp != nil && lp.closing {
		h.mu.Unlock()
		return LogSubscription{}, fmt.Errorf("log_source_cleanup_pending")
	}
	fresh := lp == nil
	if fresh {
		if len(h.profiles) >= LogMaxProfiles {
			h.mu.Unlock()
			return LogSubscription{}, fmt.Errorf("log_profile_limit")
		}
		lp = &logProfile{viewers: map[int]chan protocol.SparkLogRecord{}}
		h.profiles[p.ID] = lp
	}
	h.next++
	id := h.next
	ch := make(chan protocol.SparkLogRecord, 64)
	lp.viewers[id] = ch
	h.viewers++
	tail := append([]protocol.SparkLogRecord(nil), lp.records...)
	for len(tail) > 200 {
		tail = tail[1:]
	}
	size := 0
	for i := len(tail) - 1; i >= 0; i-- {
		size += len(tail[i].Text) + 1
		if size > 128<<10 {
			tail = tail[i+1:]
			break
		}
	}
	if fresh {
		streamCtx, cancel := context.WithCancel(context.Background())
		lp.cancel = cancel
		go h.collect(streamCtx, p, s, lp)
	}
	h.mu.Unlock()
	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if cur := h.profiles[p.ID]; cur != nil {
				if v, ok := cur.viewers[id]; ok {
					delete(cur.viewers, id)
					close(v)
					h.viewers--
				}
				if len(cur.viewers) == 0 {
					cur.cancel()
					cur.closing = true
					cur.records = nil
					cur.used = 0
				}
			}
		})
	}
	go func() { <-ctx.Done(); closeFn() }()
	return LogSubscription{Records: ch, Tail: tail, Close: closeFn}, nil
}
func (h *LogHub) collect(ctx context.Context, p Profile, s Supervisor, source *logProfile) {
	defer func() {
		h.mu.Lock()
		if h.profiles[p.ID] == source {
			delete(h.profiles, p.ID)
		}
		h.mu.Unlock()
	}()
	r, e := newRedactor(p)
	if e != nil {
		h.CloseProfile(p.ID, "redaction_failed")
		return
	}
	reader, e := s.Logs(ctx, p)
	if e != nil {
		h.CloseProfile(p.ID, "log_source_failed")
		return
	}
	defer reader.Close()
	go func() { <-ctx.Done(); reader.Close() }()
	assembler := RecordAssembler{}
	redactionFailed := false
	emit := func(line []byte, gap bool) {
		if redactionFailed {
			return
		}
		if h.appendRedacted(p.ID, r, line, gap) != nil {
			redactionFailed = true
			h.CloseProfile(p.ID, "redaction_changed")
			return
		}
	}
	buffer := make([]byte, 4096)
	for {
		n, e := reader.Read(buffer)
		if n > 0 {
			assembler.Feed(buffer[:n], emit)
		}
		if redactionFailed {
			return
		}
		if e != nil {
			assembler.Finish(emit)
			h.CloseProfile(p.ID, "log_source_closed")
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}
func (h *LogHub) appendRedacted(id string, r *Redactor, line []byte, gap bool) error {
	if r.existingGuard != nil {
		if e := r.existingGuard(); e != nil {
			return e
		}
	}
	h.append(id, r.Clean(line), gap)
	return nil
}
func (h *LogHub) append(id, text string, gap bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.profiles[id]
	if p == nil {
		return
	}
	p.seq++
	record := protocol.SparkLogRecord{Sequence: p.seq, At: time.Now().UTC(), Text: strings.Clone(text), Gap: gap}
	size := len(record.Text) + 1
	for (p.used+size > LogProfileBytes || len(p.records) >= 4096) && len(p.records) > 0 {
		p.used -= len(p.records[0].Text) + 1
		p.records[0] = protocol.SparkLogRecord{}
		p.records = p.records[1:]
	}
	p.records = append(p.records, record)
	p.used += size
	for viewer, ch := range p.viewers {
		select {
		case ch <- record:
		default:
			close(ch)
			delete(p.viewers, viewer)
			h.viewers--
		}
	}
	if len(p.viewers) == 0 {
		p.cancel()
		p.closing = true
		p.records = nil
		p.used = 0
	}
}
func (h *LogHub) CloseProfile(id, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.profiles[id]; p != nil {
		if p.closing {
			return
		}
		p.cancel()
		for _, ch := range p.viewers {
			select {
			case ch <- protocol.SparkLogRecord{At: time.Now().UTC(), Text: "[log stream closed: " + reason + "]", Gap: true}:
			default:
			}
			close(ch)
			h.viewers--
		}
		p.viewers = map[int]chan protocol.SparkLogRecord{}
		p.closing = true
		p.records = nil
		p.used = 0
	}
}
func (h *LogHub) CloseAll(reason string) {
	h.mu.Lock()
	ids := []string{}
	for id := range h.profiles {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.CloseProfile(id, reason)
	}
}
