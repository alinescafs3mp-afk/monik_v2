package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type SparkSnapshot struct {
	AgentID        string                    `json:"agent_id"`
	AgentSession   string                    `json:"agent_session"`
	Sequence       int64                     `json:"sequence"`
	ObservedAt     time.Time                 `json:"observed_at"`
	ReceivedAt     time.Time                 `json:"received_at"`
	Spark          protocol.SparkExtensionV1 `json:"spark"`
	Classification string                    `json:"classification"`
}

func (s *Store) AgentFlavor(id string) (string, error) {
	var flavor string
	err := s.db().QueryRow(`SELECT flavor FROM agent_flavors WHERE agent_id=?`, id).Scan(&flavor)
	if errors.Is(err, sql.ErrNoRows) {
		return "general", nil
	}
	return flavor, err
}

func (s *Store) SparkSnapshot(id string) (*SparkSnapshot, error) {
	var observed, received, payload string
	classification, e := s.SparkClassification(id)
	if e != nil {
		return nil, e
	}
	v := &SparkSnapshot{AgentID: id, Classification: classification}
	err := s.db().QueryRow(`SELECT session_id,seq,observed_at,received_at,payload FROM spark_latest WHERE agent_id=?`, id).Scan(&v.AgentSession, &v.Sequence, &observed, &received, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		if classification == "spark" {
			ag, e := s.Agent(id)
			if e != nil {
				return nil, e
			}
			v.AgentSession = ag.SessionID
			v.Sequence = ag.LastSeq
			if ag.LastLiveAt != nil {
				v.ReceivedAt = *ag.LastLiveAt
			}
			_, at, e := s.LatestHost(id)
			if e != nil && !errors.Is(e, ErrNotFound) {
				return nil, e
			}
			v.ObservedAt = at
			v.Spark = protocol.SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unknown", Metrics: []protocol.SparkMetric{}}
			return v, nil
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if v.ObservedAt, err = time.Parse(time.RFC3339Nano, observed); err != nil {
		return nil, err
	}
	if v.ReceivedAt, err = time.Parse(time.RFC3339Nano, received); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(payload), &v.Spark); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Store) SparkClassification(id string) (string, error) {
	var classification string
	e := s.db().QueryRow(`SELECT classification FROM spark_classification WHERE agent_id=?`, id).Scan(&classification)
	if e == nil {
		return classification, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	var hardware, extension int
	if e = s.db().QueryRow(`SELECT EXISTS(SELECT 1 FROM spark_hardware_evidence WHERE agent_id=?),EXISTS(SELECT 1 FROM spark_latest WHERE agent_id=?)`, id, id).Scan(&hardware, &extension); e != nil {
		return "", e
	}
	if hardware != 0 {
		return "spark", nil
	}
	if extension != 0 {
		return "unknown", nil
	}
	return "", nil
}

func (s *Store) SetSparkClassification(id, classification string) error {
	if classification != "spark" && classification != "other" {
		return fmt.Errorf("classification must be spark or other")
	}
	if _, err := s.Agent(id); err != nil {
		return err
	}
	_, err := s.db().Exec(`INSERT INTO spark_classification(agent_id,classification) VALUES(?,?) ON CONFLICT(agent_id) DO UPDATE SET classification=excluded.classification`, id, classification)
	return err
}

func (s *Store) acceptSpark(rep protocol.AgentReport, live bool) error {
	b, err := json.Marshal(rep.Spark)
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return fmt.Errorf("Spark extension exceeds byte budget")
	}
	if _, err = s.db().Exec(`INSERT INTO spark_samples(agent_id,session_id,seq,observed_at,payload) VALUES(?,?,?,?,?)`, rep.AgentID, rep.SessionID, rep.Sequence, rep.ObservedAt.UTC().Format(dbTimeFormat), string(b)); err != nil {
		return err
	}
	if !live {
		return nil
	}
	current, err := s.SparkSnapshot(rep.AgentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	merged := *rep.Spark
	if current != nil {
		merged = current.Spark
		if current.AgentSession != rep.SessionID {
			merged.ControlState = nil
		}
		merged.SchemaVersion = rep.Spark.SchemaVersion
		merged.MemoryTopology = rep.Spark.MemoryTopology
		metrics := map[string]protocol.SparkMetric{}
		for _, m := range merged.Metrics {
			metrics[m.MetricID] = m
		}
		for _, m := range rep.Spark.Metrics {
			old, ok := metrics[m.MetricID]
			if !ok || m.Observation.ObservedAt.After(old.Observation.ObservedAt) {
				metrics[m.MetricID] = m
			}
		}
		merged.Metrics = []protocol.SparkMetric{}
		for _, m := range metrics {
			merged.Metrics = append(merged.Metrics, m)
		}
		sort.Slice(merged.Metrics, func(i, j int) bool { return merged.Metrics[i].MetricID < merged.Metrics[j].MetricID })
		if i := rep.Spark.Inventory; i != nil && (merged.Inventory == nil || i.ObservedAt.After(merged.Inventory.ObservedAt)) {
			inventory := *i
			if !i.Complete && merged.Inventory != nil {
				profiles := map[string]protocol.SparkProfile{}
				for _, p := range merged.Inventory.Profiles {
					profiles[p.ProfileID] = p
				}
				for _, p := range i.Profiles {
					profiles[p.ProfileID] = p
				}
				for _, id := range i.RemovedProfileIDs {
					delete(profiles, id)
				}
				inventory.Profiles = []protocol.SparkProfile{}
				for _, p := range profiles {
					inventory.Profiles = append(inventory.Profiles, p)
				}
				sort.Slice(inventory.Profiles, func(a, b int) bool { return inventory.Profiles[a].ProfileID < inventory.Profiles[b].ProfileID })
			}
			merged.Inventory = &inventory
		}
		states := map[string]protocol.SparkModelState{}
		for _, st := range merged.ModelStates {
			states[st.ProfileID] = st
		}
		for _, st := range rep.Spark.ModelStates {
			old, ok := states[st.ProfileID]
			if !ok || st.ObservedAt.After(old.ObservedAt) {
				states[st.ProfileID] = st
			}
		}
		merged.ModelStates = nil
		for _, st := range states {
			merged.ModelStates = append(merged.ModelStates, st)
		}
		sort.Slice(merged.ModelStates, func(i, j int) bool { return merged.ModelStates[i].ProfileID < merged.ModelStates[j].ProfileID })
		if c := rep.Spark.ControlState; c != nil && (merged.ControlState == nil || c.ObservedAt.After(merged.ControlState.ObservedAt)) {
			merged.ControlState = c
		}
	}
	// Bound accumulated delta inventory/metric unions too, not only each packet.
	if len(merged.Metrics) > 512 || len(merged.ModelStates) > 64 || merged.Inventory != nil && len(merged.Inventory.Profiles) > 64 {
		return fmt.Errorf("merged Spark state exceeds budget")
	}
	if merged.Inventory != nil {
		present := map[string]bool{}
		for _, p := range merged.Inventory.Profiles {
			present[p.ProfileID] = true
		}
		kept := merged.ModelStates[:0]
		for _, st := range merged.ModelStates {
			if present[st.ProfileID] {
				kept = append(kept, st)
			}
		}
		merged.ModelStates = kept
	}
	b, err = json.Marshal(merged)
	if err != nil {
		return err
	}
	_, err = s.db().Exec(`INSERT INTO spark_latest(agent_id,session_id,seq,observed_at,received_at,payload) VALUES(?,?,?,?,?,?) ON CONFLICT(agent_id) DO UPDATE SET session_id=excluded.session_id,seq=excluded.seq,observed_at=excluded.observed_at,received_at=excluded.received_at,payload=excluded.payload`, rep.AgentID, rep.SessionID, rep.Sequence, rep.ObservedAt.UTC().Format(dbTimeFormat), s.now().UTC().Format(dbTimeFormat), string(b))
	return err
}

func (s *Store) SparkHistory(id string, from, to time.Time, limit int) ([]SparkSnapshot, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db().Query(`SELECT session_id,seq,observed_at,payload FROM spark_samples WHERE agent_id=? AND observed_at>=? AND observed_at<=? ORDER BY observed_at DESC,session_id,seq LIMIT ?`, id, from.UTC().Format(dbTimeFormat), to.UTC().Format(dbTimeFormat), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SparkSnapshot{}
	bytes := 0
	for rows.Next() {
		v := SparkSnapshot{AgentID: id}
		var at, raw string
		if err = rows.Scan(&v.AgentSession, &v.Sequence, &at, &raw); err != nil {
			return nil, err
		}
		bytes += len(raw)
		if bytes > 8<<20 {
			return nil, fmt.Errorf("Spark history exceeds byte budget; choose shorter range")
		}
		if v.ObservedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v.Spark); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
