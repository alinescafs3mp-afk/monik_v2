package spark

import (
	"context"
	"encoding/json"
	"strings"
)

// EngineReadiness is passive. It has no generation/completion route and never
// invokes SGLang health_generate. A service-level probe does not claim that an
// individual on-demand model is resident.
func EngineReadiness(ctx context.Context, p Profile) (string, string) {
	if p.HealthURL == "" {
		return "unknown", "passive_probe_not_configured"
	}
	if p.HealthScope != "deployment" {
		return "unknown", "service_health_is_not_model_readiness"
	}
	r, e := PassiveHealth(ctx, p.HealthURL, 16<<10)
	if e != nil {
		return "unknown", "passive_probe_unavailable"
	}
	if r.Status == 503 {
		return "unready", "engine_loading_or_unready"
	}
	if r.Status != 200 {
		return "unready", "passive_health_failed"
	}
	switch p.Engine {
	case "llama.cpp":
		var body struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(r.Body, &body) == nil && body.Status == "ok" {
			return "ready", ""
		}
		return "unknown", "unexpected_health_body"
	case "vllm", "sglang": // These documented endpoints may return an empty body.
		if len(strings.TrimSpace(string(r.Body))) == 0 {
			return "ready", ""
		}
		var body map[string]any
		if json.Unmarshal(r.Body, &body) == nil {
			if s, ok := body["status"].(string); ok && member(strings.ToLower(s), "ok", "healthy", "ready") {
				return "ready", ""
			}
		}
		return "unknown", "unexpected_health_body"
	case "custom", "tensorrt-llm": // Explicit locally configured fixed probe, no guessed schema.
		var body struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(r.Body, &body) == nil && body.Status == "ok" {
			return "ready", ""
		}
		return "unknown", "custom_health_schema_unverified"
	default:
		return "unknown", "engine_adapter_not_verified"
	}
}
