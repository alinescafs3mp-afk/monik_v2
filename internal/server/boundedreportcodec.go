package server

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
)

const maxReportBytes = 4 << 20

// Authentication runs before this decoder; handleReport authorizes again at
// its existing serialized commit boundary. Compression is a report transport
// detail and never changes the JSON/schema or the immutable observation hash.
func parseReportJSON(r *http.Request, dst any) error {
	values := r.Header.Values("Content-Encoding")
	if len(values) > 1 {
		return fmt.Errorf("ambiguous report encoding")
	}
	encoding := ""
	if len(values) == 1 {
		encoding = strings.ToLower(strings.TrimSpace(values[0]))
	}
	switch encoding {
	case "", "identity":
		return parseJSONLimit(r, dst, maxReportBytes)
	case "gzip":
	default:
		return fmt.Errorf("unsupported report encoding")
	}
	encoded, err := io.ReadAll(io.LimitReader(r.Body, maxReportBytes+1))
	if err != nil || len(encoded) > maxReportBytes {
		return fmt.Errorf("compressed report read failed or exceeds limit")
	}
	source := bytes.NewReader(encoded)
	zip, err := gzip.NewReader(source)
	if err != nil {
		return fmt.Errorf("invalid compressed report")
	}
	zip.Multistream(false)
	decoded, readErr := io.ReadAll(io.LimitReader(zip, maxReportBytes+1))
	closeErr := zip.Close()
	// Reading through EOF verifies the gzip checksum and length. A ByteReader
	// leaves the source at the exact first-member boundary; no extra member or
	// arbitrary trailing byte can hide behind an otherwise valid JSON object.
	if readErr != nil || closeErr != nil || len(decoded) > maxReportBytes || source.Len() != 0 {
		return fmt.Errorf("incomplete, oversized or trailing compressed report")
	}
	decoded = bytes.TrimSpace(decoded)
	if len(decoded) == 0 || decoded[0] != '{' {
		return fmt.Errorf("report JSON object required")
	}
	return jsonutil.Unmarshal(decoded, dst)
}
