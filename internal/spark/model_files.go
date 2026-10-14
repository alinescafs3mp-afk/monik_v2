package spark

import (
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type modelFileMetadata struct {
	Count               int
	Logical, Allocated  uint64
	Complete, Available bool
	At                  time.Time
}

// Metadata only, no weight reads/hashing. Directory enumeration is batched and
// descriptor-relative; a huge single directory cannot allocate an unbounded
// WalkDir listing. Symlinked weights require an explicitly approved real root.
func scanModelFiles(paths []string) modelFileMetadata {
	out := modelFileMetadata{Complete: true, At: time.Now().UTC()}
	seen := map[string]bool{}
	visited := 0
	add := func(stat *unix.Stat_t) {
		key := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
		if seen[key] {
			return
		}
		seen[key] = true
		out.Count++
		out.Logical += uint64(stat.Size)
		out.Allocated += uint64(stat.Blocks) * 512
		out.Available = true
	}
	var walk func(*os.File, int)
	walk = func(directory *os.File, depth int) {
		defer directory.Close()
		for {
			remaining := 4096 - visited
			if remaining <= 0 {
				extra, e := directory.ReadDir(1)
				if len(extra) > 0 || e != io.EOF {
					out.Complete = false
				}
				return
			}
			entries, e := directory.ReadDir(min(128, remaining))
			if e != nil && e != io.EOF {
				out.Complete = false
				return
			}
			for _, entry := range entries {
				if visited >= 4096 {
					out.Complete = false
					return
				}
				visited++
				var stat unix.Stat_t
				if unix.Fstatat(int(directory.Fd()), entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW) != nil {
					out.Complete = false
					continue
				}
				kind := stat.Mode & unix.S_IFMT
				if kind == unix.S_IFDIR {
					if depth >= 3 {
						out.Complete = false
						continue
					}
					fd, err := unix.Openat(int(directory.Fd()), entry.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
					if err != nil {
						out.Complete = false
						continue
					}
					walk(os.NewFile(uintptr(fd), entry.Name()), depth+1)
					continue
				}
				name := strings.ToLower(entry.Name())
				if !strings.HasSuffix(name, ".safetensors") && !strings.HasSuffix(name, ".gguf") && !strings.HasSuffix(name, ".bin") && !strings.HasSuffix(name, ".pt") {
					continue
				}
				if kind != unix.S_IFREG {
					out.Complete = false
					continue
				}
				add(&stat)
			}
			if e == io.EOF {
				return
			}
		}
	}
	for _, root := range paths {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			out.Complete = false
			continue
		}
		fd, e := openModelMetadataRoot(root)
		if e != nil {
			out.Complete = false
			continue
		}
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil {
			unix.Close(fd)
			out.Complete = false
			continue
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFREG {
			add(&stat)
			unix.Close(fd)
			continue
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(fd)
			out.Complete = false
			continue
		}
		readFD, e := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if e != nil {
			out.Complete = false
			continue
		}
		walk(os.NewFile(uintptr(readFD), root), 0)
	}
	return out
}

// Resolve each approved component by descriptor without following any symlink,
// including an ancestor. O_PATH permits metadata on regular roots without
// opening weights, devices or potentially blocking FIFOs for reading.
func openModelMetadataRoot(root string) (int, error) {
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil || root == "/" {
		return fd, err
	}
	parts := strings.Split(strings.TrimPrefix(root, "/"), "/")
	for n, part := range parts {
		flags := unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if n < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if openErr != nil {
			return -1, openErr
		}
		fd = next
	}
	return fd, nil
}
func (m modelFileMetadata) settings() []protocol.SparkSetting {
	provenance := "unknown"
	var count, logical, allocated any
	if m.Complete {
		provenance = "observed-effective"
		count = m.Count
		logical = float64(m.Logical)
		allocated = float64(m.Allocated)
	}
	return []protocol.SparkSetting{{Name: "model_file_count", Value: count, Provenance: provenance}, {Name: "model_files_logical_bytes", Value: logical, Provenance: provenance}, {Name: "model_files_allocated_bytes", Value: allocated, Provenance: provenance}}
}
