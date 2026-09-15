package protocol

import (
	"encoding/json"
	"time"
)

// ObservationPayload is the immutable part of a report. Live/backfill status,
// transmission time and separately acknowledged job receipts may change when
// retrying; measurements, identity and their timestamps must never change.
func ObservationPayload(r AgentReport) ([]byte, error) {
	r.IsLive = false
	r.ReportedAt = time.Time{}
	r.JobReceipts = nil
	return json.Marshal(r)
}
