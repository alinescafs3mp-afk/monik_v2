package server

import (
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"time"
)

func actionAvailability() map[string]string {
	out := map[string]string{}
	for id := range actions.Registry {
		if why := actions.UnavailableReason(id); why != "" {
			out[id] = why
		}
	}
	return out
}
func contact(ag *storage.AgentRow, now time.Time) (string, string) {
	if ag.Revoked {
		return "revoked", "Регистрация отозвана"
	}
	if ag.Archived {
		return "archived", "В архиве"
	}
	return rules.AgentFreshness(ag.LastLiveAt, now)
}

type serviceSummary struct {
	InventoryArchived bool  `json:"inventory_archived"`
	MonitoringEnabled bool  `json:"monitoring_enabled"`
	MonitoringApplied bool  `json:"monitoring_applied"`
	ConfigRevision    int64 `json:"configuration_revision"`
	GlobalPaused      bool  `json:"globally_paused"`
	HasCheck          bool  `json:"has_check"`
	*storage.ServiceRow
	Observation       *protocol.CheckObservation   `json:"observation"`
	AgentState        string                       `json:"agent_state"`
	State             string                       `json:"state"`
	Summary           string                       `json:"summary"`
	Discovery         *protocol.DiscoveredEndpoint `json:"discovery,omitempty"`
	Fresh             bool                         `json:"fresh"`
	MaintenanceActive bool                         `json:"maintenance_active"`
}

func (a *App) serviceSummaries(agentID string, now time.Time) ([]serviceSummary, error) {
	rows, err := a.Store.Services(agentID)
	if err != nil {
		return nil, err
	}
	out := make([]serviceSummary, 0, len(rows))
	contacts := map[string]string{}
	desiredChecks := map[string]protocol.CheckDefinition{}
	desiredRev := map[string]int64{}
	globalPaused := map[string]bool{}
	configConfirmed := map[string]bool{}
	for _, sv := range rows {
		st, known := contacts[sv.AgentID]
		if !known {
			ag, err := a.Store.Agent(sv.AgentID)
			if err != nil {
				return nil, err
			}
			st, _ = contact(ag, now)
			contacts[sv.AgentID] = st
			var cfg protocol.AgentConfig
			if ag.DesiredConfig != "" {
				if err = json.Unmarshal([]byte(ag.DesiredConfig), &cfg); err != nil {
					return nil, fmt.Errorf("invalid desired configuration")
				}
			}
			globalPaused[sv.AgentID] = cfg.Paused
			configConfirmed[sv.AgentID] = ag.DesiredRevision == ag.AppliedRevision && ag.DesiredHash == ag.AppliedHash
			for _, d := range cfg.Checks {
				desiredChecks[d.ServiceID] = d
			}
			desiredRev[sv.AgentID] = ag.DesiredRevision
		}
		obs, err := a.Store.LatestCheckObs(sv.ID)
		if err != nil && err != storage.ErrNotFound {
			return nil, err
		}
		item := serviceSummary{ServiceRow: sv, Observation: obs, AgentState: st, State: "unknown", Summary: "Нет измерений"}
		item.Discovery, err = a.Store.ServiceDiscovery(sv.ID)
		if err != nil {
			return nil, err
		}
		if obs != nil {
			item.Fresh = st == "ok" && now.Sub(obs.ObservedAt) <= protocol.CheckFreshness(obs.IntervalSeconds) && !obs.ObservedAt.After(now.Add(5*time.Second))
			item.State = "responds"
			item.Summary = "HTTP отвечает; здоровье приложения не настроено"
			if obs.HTTPStatus != nil {
				item.Summary = fmt.Sprintf("HTTP %d", *obs.HTTPStatus)
				if obs.LatencyMS != nil {
					item.Summary += fmt.Sprintf(" · %.0f мс", *obs.LatencyMS)
				}
			}
			switch {
			case !item.Fresh:
				item.State = "stale"
				item.Summary += " · нет свежих данных"
			case obs.Quality != protocol.QualityOK:
				item.State = "unknown"
				item.Summary = string(obs.Quality) + " · " + obs.AppReason
			case obs.Transport != "ok":
				item.State = "transport_fail"
				item.Summary = obs.Transport
				if obs.AppReason != "" {
					item.Summary += " · " + obs.AppReason
				}
				if obs.TLSReason != "" {
					item.Summary += " · " + obs.TLSReason
				}
			case obs.AppResult == "fail":
				item.State = "app_fail"
				item.Summary += " · " + obs.AppReason
			case obs.AppResult != "pass" && obs.HTTPStatus != nil && *obs.HTTPStatus >= 500:
				item.State = "http_error"
				item.Summary += " · ошибка сервера"
			case obs.AppResult == "pass":
				item.State = "ok"
				item.Summary += " · проверка пройдена"
			default:
				item.Summary += " · здоровье приложения не настроено"
			}
		}
		if obs != nil && obs.Feedback != nil && obs.Feedback.Health != "" {
			item.Summary += " · ответ: " + obs.Feedback.HealthSource + "=" + obs.Feedback.Health
		}
		d, hasCheck := desiredChecks[sv.ID]
		item.HasCheck = hasCheck
		item.ConfigRevision = desiredRev[sv.AgentID]
		item.GlobalPaused = globalPaused[sv.AgentID]
		item.MonitoringEnabled = hasCheck && !d.Paused && !d.Ignored
		item.MonitoringApplied = configConfirmed[sv.AgentID]
		if !hasCheck && obs == nil {
			item.State = "unmonitored"
			item.Summary = "Обнаружен; периодическая проверка не настроена"
			item.Fresh = false
		}
		if obs != nil && hasCheck && (!configConfirmed[sv.AgentID] || obs.ConfigRev != desiredRev[sv.AgentID] || obs.CheckID != d.ID) {
			item.State = "pending"
			item.Fresh = false
			item.Summary = "Ожидаем результат актуальной конфигурации; предыдущий ответ не оценивает новый запрос"
		}
		if globalPaused[sv.AgentID] || sv.Paused || sv.Ignored || d.Paused || d.Ignored {
			if configConfirmed[sv.AgentID] {
				item.State = "paused"
				item.Summary = "Проверки приостановлены (конфигурация подтверждена)"
			} else {
				item.State = "pending"
				item.Summary += " · пауза запрошена, ждём подтверждения агента"
			}
			item.Fresh = false
		} else if obs != nil && obs.Quality == protocol.QualityPaused {
			item.State = "paused"
			item.Summary = "Последний отчёт: проверки приостановлены"
			item.Fresh = false
		}
		item.InventoryArchived = sv.InventoryState == "missing" && storage.IsListenerService(sv) && !item.MonitoringEnabled && !sv.Pinned && (!hasCheck || item.MonitoringApplied)
		if item.InventoryArchived {
			item.State = "inactive"
			item.Summary = "Порт больше не обнаруживается; периодическая проверка выключена"
			item.Fresh = false
		} else if sv.InventoryState == "missing" {
			item.Summary += " · порт не обнаруживается (история и выбранная проверка сохранены)"
		}
		item.MaintenanceActive, err = a.Store.InMaintenance("service", sv.ID, now)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// Desired-state progress is shown separately from observed service failures.
func serviceHasProblem(state string) bool {
	switch state {
	case "ok", "responds", "paused", "unmonitored", "pending", "inactive":
		return false
	default:
		return true
	}
}

// Do not filter storage reads: names, config and history stay addressable by ID.
func currentServices(rows []serviceSummary) ([]serviceSummary, int) {
	out := make([]serviceSummary, 0, len(rows))
	inactive := 0
	for _, r := range rows {
		if r.InventoryArchived {
			inactive++
			continue
		}
		out = append(out, r)
	}
	return out, inactive
}
