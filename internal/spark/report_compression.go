package spark

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"
)

const (
	maxReportBytes          = 4 << 20
	minCompressedReportSize = 1 << 10
)

// The capability is process-local and learned only from a verified committed
// report ACK. In particular, enrollment, health and version hints do not count.
func reportGzipAdvertised(header http.Header) bool {
	values := header.Values("Accept-Encoding")
	return len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "gzip")
}

type reportCompressionBuffer struct {
	bytes.Buffer
	limit int
}

func (b *reportCompressionBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		return 0, fmt.Errorf("report compression does not reduce size")
	}
	return b.Buffer.Write(data)
}

// Compress the exact marshaled bytes, keeping the immutable envelope and spool
// unchanged. Both the source and destination have finite 4 MiB ceilings; the
// destination stops growing as soon as compression cannot be a strict saving.
func encodeReportBody(data []byte, supported bool) ([]byte, string, error) {
	if len(data) > maxReportBytes {
		return nil, "", fmt.Errorf("report exceeds byte limit")
	}
	if !supported || len(data) < minCompressedReportSize {
		return data, "", nil
	}
	out := &reportCompressionBuffer{limit: len(data) - 1}
	zip, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		return nil, "", err
	}
	_, writeErr := zip.Write(data)
	closeErr := zip.Close()
	if writeErr != nil || closeErr != nil || out.Len() >= len(data) {
		return data, "", nil
	}
	return out.Bytes(), "gzip", nil
}
