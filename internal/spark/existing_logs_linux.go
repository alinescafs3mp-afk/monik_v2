//go:build linux

package spark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const existingLogReadBudget = 128 << 10
const existingLogRotationLimit = 64

// Every component is opened relative to its held parent, never checked and then
// reopened by pathname. Production always requires root ownership. The owner
// argument exists so offline fixtures exercise this same descriptor code.
func openExistingLogFile(path string, owner uint32, private bool) (*os.File, unix.Stat_t, error) {
	var st unix.Stat_t
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, st, fmt.Errorf("log_source_path_invalid")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, st, fmt.Errorf("log_source_unavailable")
	}
	defer func() {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || (st.Uid != 0 && st.Uid != owner) || st.Mode&0022 != 0 {
			return nil, st, fmt.Errorf("log_source_ancestor_unsafe")
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		if e != nil {
			return nil, st, fmt.Errorf("log_source_unavailable")
		}
		_ = unix.Close(fd)
		fd = next
	}
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != owner || st.Mode&0022 != 0 || st.Nlink != 1 || private && st.Mode&0777 != 0600 {
		return nil, st, fmt.Errorf("log_source_owner_type_mode_unsafe")
	}
	f := os.NewFile(uintptr(fd), path)
	fd = -1
	return f, st, nil
}

func pinnedExistingLogRedactor(path, pin string, owner uint32) (*Redactor, error) {
	f, before, e := openExistingLogFile(path, owner, true)
	if e != nil {
		return nil, fmt.Errorf("redaction_source_unavailable")
	}
	defer f.Close()
	if before.Size < 1 || before.Size > 64<<10 || !bareHash(pin) {
		return nil, fmt.Errorf("redaction_source_invalid")
	}
	b, e := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	var after unix.Stat_t
	if e != nil || len(b) > 64<<10 || unix.Fstat(int(f.Fd()), &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || before.Mode != after.Mode || before.Uid != after.Uid || before.Nlink != after.Nlink {
		return nil, fmt.Errorf("redaction_source_changed")
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != pin || bytes.IndexByte(b, 0) >= 0 || bytes.ContainsAny(b, "\r") {
		return nil, fmt.Errorf("redaction_source_pin_invalid")
	}
	r := &Redactor{}
	for _, value := range strings.Split(string(b), "\n") {
		// Values-only format: no environment or shell syntax is evaluated. Values
		// are exact bytes; surrounding whitespace must never alter a key silently.
		if value == "" {
			continue
		}
		if strings.TrimSpace(value) != value || len(value) > LogMaxLine || len(r.secrets) >= 512 {
			return nil, fmt.Errorf("redaction_value_invalid")
		}
		r.secrets = append(r.secrets, value)
	}
	if len(r.secrets) == 0 {
		return nil, fmt.Errorf("redaction_values_required")
	}
	// The guard must use the exact fd witness used for this verified read. A
	// separate pathname open must never adopt a replacement as its baseline.
	r.existingGuard, e = existingRedactionGuard(path, owner, after)
	if e != nil {
		return nil, e
	}
	return r, nil
}

func existingLogRedactor(p Profile) (*Redactor, error) {
	if !p.LogEnabled || !validExistingLogApproval(p) || p.validateExistingCoordinator() != nil || os.Geteuid() != 0 {
		return nil, fmt.Errorf("logs_not_locally_approved")
	}
	return pinnedExistingLogRedactor(p.Target.Existing.RedactionFile, p.Target.Existing.RedactionSHA256, 0)
}

type existingFileStream struct {
	path      string
	name      string
	file      *os.File
	stat      unix.Stat_t
	offset    int64
	assembler RecordAssembler
}
type existingFileLogReader struct {
	*io.PipeReader
	cancel context.CancelFunc
	once   sync.Once
}

func (r *existingFileLogReader) Close() error {
	r.once.Do(func() { r.cancel(); _ = r.PipeReader.Close() })
	return nil
}

func existingLogs(ctx context.Context, p Profile) (LogReader, error) {
	if os.Geteuid() != 0 || !p.LogEnabled || p.validateExistingCoordinator() != nil {
		return nil, fmt.Errorf("logs_not_locally_approved")
	}
	// Verify the pinned values even for callers outside LogHub. LogHub performs
	// filtering with its own held immutable copy before buffering/transmission.
	redactor, e := existingLogRedactor(p)
	if e != nil {
		return nil, e
	}
	return newExistingFileLogReader(ctx, existingLogRoot, p.ID, 0, 250*time.Millisecond, redactor.existingGuard)
}

func existingRedactionGuard(path string, owner uint32, verified ...unix.Stat_t) (func() error, error) {
	if len(verified) != 1 {
		return nil, fmt.Errorf("redaction_guard_requires_pinned_read")
	}
	before := verified[0]
	guard := func() error {
		f, after, e := openExistingLogFile(path, owner, true)
		if e != nil {
			return fmt.Errorf("redaction_source_changed")
		}
		_ = f.Close()
		if before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || before.Mode != after.Mode || before.Uid != after.Uid || before.Nlink != after.Nlink {
			return fmt.Errorf("redaction_source_changed")
		}
		return nil
	}
	if e := guard(); e != nil {
		return nil, e
	}
	return guard, nil
}

// The source path is constructed by the fixed adapter, never from an RPC field.
func newExistingFileLogReader(ctx context.Context, root, id string, owner uint32, poll time.Duration, guard func() error) (LogReader, error) {
	if !member(id, existingIDs...) {
		return nil, fmt.Errorf("log_profile_unapproved")
	}
	streams := []*existingFileStream{}
	for _, name := range []string{"engine", "actions"} {
		path := filepath.Join(root, id, name+".log")
		f, st, e := openExistingLogFile(path, owner, false)
		if e != nil {
			for _, stream := range streams {
				_ = stream.file.Close()
			}
			return nil, e
		}
		streams = append(streams, &existingFileStream{path: path, name: name, file: f, stat: st})
	}
	streamCtx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	r := &existingFileLogReader{PipeReader: reader, cancel: cancel}
	go func() { <-streamCtx.Done(); _ = writer.CloseWithError(streamCtx.Err()) }()
	go func() {
		defer cancel()
		defer writer.Close()
		defer func() {
			for _, s := range streams {
				_ = s.file.Close()
			}
		}()
		err := followExistingLogFiles(streamCtx, writer, streams, owner, poll, guard)
		if err != nil {
			_ = writer.CloseWithError(err)
		}
	}()
	return r, nil
}

func followExistingLogFiles(ctx context.Context, writer *io.PipeWriter, streams []*existingFileStream, owner uint32, poll time.Duration, guard func() error) error {
	if guard != nil {
		if e := guard(); e != nil {
			return e
		}
	}
	emit := func(s *existingFileStream, line []byte, gap bool) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if guard != nil {
			if e := guard(); e != nil {
				return e
			}
		}
		if gap {
			_, e := writer.Write(append(append([]byte(nil), line...), '\n'))
			return e
		}
		// Each source assembles complete records independently. Chunks from two
		// files can therefore never combine into a partially redacted record.
		b := append([]byte("["+s.name+"] "), line...)
		b = append(b, '\n')
		_, e := writer.Write(b)
		return e
	}
	// Scan only the final 128KiB of each file. Retain at most 200 records and
	// 128KiB across BOTH sources for the initial profile tail.
	tail := [][]byte{}
	tailBytes := 0
	for _, s := range streams {
		start := s.stat.Size - existingLogReadBudget
		if start < 0 {
			start = 0
		}
		raw := make([]byte, int(s.stat.Size-start))
		n, e := s.file.ReadAt(raw, start)
		if e != nil && e != io.EOF {
			return fmt.Errorf("log_tail_unavailable")
		}
		var after unix.Stat_t
		if unix.Fstat(int(s.file.Fd()), &after) != nil || after.Dev != s.stat.Dev || after.Ino != s.stat.Ino || after.Uid != owner || after.Mode&0022 != 0 || after.Mode&unix.S_IFMT != unix.S_IFREG || after.Nlink != 1 {
			return fmt.Errorf("log_source_changed")
		}
		raw = raw[:n]
		s.offset = start + int64(n)
		if start > 0 {
			if i := bytes.IndexByte(raw, '\n'); i >= 0 {
				raw = raw[i+1:]
			} else {
				raw = nil
				s.assembler.dropping = true
			}
		}
		s.assembler.Feed(raw, func(line []byte, gap bool) {
			b := append([]byte("["+s.name+"] "), line...)
			if gap {
				b = append([]byte(nil), line...)
			}
			if len(b) > LogMaxLine {
				b = []byte("[oversized log record dropped]")
			}
			b = append(b, '\n')
			tail = append(tail, b)
			tailBytes += len(b)
			for len(tail) > 200 || tailBytes > existingLogReadBudget {
				tailBytes -= len(tail[0])
				tail[0] = nil
				tail = tail[1:]
			}
		})
	}
	for _, line := range tail {
		if guard != nil {
			if e := guard(); e != nil {
				return e
			}
		}
		if _, e := writer.Write(line); e != nil {
			return e
		}
	}
	tail = nil
	buf := make([]byte, 4096)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	rotations := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if guard != nil {
			if e := guard(); e != nil {
				return e
			}
		}
		budget := existingLogReadBudget
		for index, s := range streams {
			current, st, e := openExistingLogFile(s.path, owner, false)
			if e != nil {
				return e
			}
			if st.Dev != s.stat.Dev || st.Ino != s.stat.Ino || st.Size < s.offset {
				rotations++
				if rotations > existingLogRotationLimit {
					_ = current.Close()
					return fmt.Errorf("log_rotation_limit")
				}
				var writeErr error
				s.assembler.Finish(func(line []byte, gap bool) {
					if writeErr == nil {
						writeErr = emit(s, line, gap)
					}
				})
				if writeErr != nil {
					_ = current.Close()
					return writeErr
				}
				_ = s.file.Close()
				s.file = current
				s.stat = st
				s.offset = 0
				if e = emit(s, []byte("[log source rotated or truncated]"), true); e != nil {
					return e
				}
			} else {
				_ = current.Close()
			}
			// An append may change size, but never source ownership/mode/inode.
			var held unix.Stat_t
			if unix.Fstat(int(s.file.Fd()), &held) != nil || held.Uid != owner || held.Mode&unix.S_IFMT != unix.S_IFREG || held.Mode&0022 != 0 || held.Nlink != 1 {
				return fmt.Errorf("log_source_changed")
			}
			// Divide fairly so a busy engine cannot starve actions or control.
			allow := budget / (len(streams) - index)
			for allow > 0 {
				want := len(buf)
				if want > allow {
					want = allow
				}
				n, e := s.file.ReadAt(buf[:want], s.offset)
				if n > 0 {
					s.offset += int64(n)
					allow -= n
					budget -= n
					var writeErr error
					s.assembler.Feed(buf[:n], func(line []byte, gap bool) {
						if writeErr == nil {
							writeErr = emit(s, line, gap)
						}
					})
					if writeErr != nil {
						return writeErr
					}
				}
				if e == io.EOF {
					break
				}
				if e != nil {
					return fmt.Errorf("log_source_read_failed")
				}
			}
		}
	}
}
