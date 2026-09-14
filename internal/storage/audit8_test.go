package storage

import (
	"testing"
	"time"
)

func TestAudit8CorruptHostIsNotSuccessfulZeroSnapshot(t *testing.T) {
	for _, payload := range []string{`{bad`, `null`} {
		t.Run(payload, func(t *testing.T) {
			s := auditStore(t)
			auditAgent(t, s, "h")
			now := time.Now().UTC()
			_, e := s.DB.Exec("INSERT INTO host_samples(agent_id,observed_at,received_at,payload) VALUES(?,?,?,?)", "h", now.Format(dbTimeFormat), now.Format(dbTimeFormat), payload)
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = s.LatestHost("h"); e == nil {
				t.Error("latest returned corrupt data as success")
			}
			if _, _, e = s.HostAt("h", now.Add(time.Second)); e == nil {
				t.Error("point returned corrupt data as success")
			}
		})
	}
}
