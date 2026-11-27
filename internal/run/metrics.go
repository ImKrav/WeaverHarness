package run

import (
	"slices"
	"strings"
	"time"
)

// ProviderStats aggregates execution counts and active duration for one provider.
type ProviderStats struct {
	Provider       string        `json:"provider"`
	Runs           int           `json:"runs"`
	Succeeded      int           `json:"succeeded"`
	QuotaErrors    int           `json:"quota_errors"`
	Timeouts       int           `json:"timeouts"`
	EmptyOutputs   int           `json:"empty_outputs"`
	ActiveDuration time.Duration `json:"active_duration_ns"`
}

// RunMetrics summarizes task progress, execution durations, and per-provider statistics.
type RunMetrics struct {
	TotalAgents    int             `json:"total_agents"`
	AcceptedTasks  int             `json:"accepted_tasks"`
	TotalTasks     int             `json:"total_tasks"`
	WallDuration   time.Duration   `json:"wall_duration_ns"`
	ActiveDuration time.Duration   `json:"active_duration_ns"`
	ReceiptRetries int             `json:"receipt_retries"`
	FollowUps      int             `json:"follow_ups"`
	ByProvider     []ProviderStats `json:"by_provider"`
}

// Summarize computes aggregate metrics for s, falling back to StartedAt/EndedAt for v1 states.
func Summarize(s *State) RunMetrics {
	if s == nil {
		return RunMetrics{}
	}

	var m RunMetrics
	if s.Plan != nil {
		m.TotalTasks = len(s.Plan.Tasks)
		for _, t := range s.Plan.Tasks {
			if t != nil && t.Status == TaskAccepted {
				m.AcceptedTasks++
			}
		}
	}

	byProv := make(map[string]*ProviderStats)
	var minStart, maxEnd time.Time

	for _, a := range s.Agents {
		if a == nil {
			continue
		}
		m.TotalAgents++
		m.ReceiptRetries += a.ReceiptRetries
		m.FollowUps += a.FollowUps

		active := agentActiveDuration(a)
		m.ActiveDuration += active

		if !a.StartedAt.IsZero() && (minStart.IsZero() || a.StartedAt.Before(minStart)) {
			minStart = a.StartedAt
		}
		if !a.EndedAt.IsZero() && (maxEnd.IsZero() || a.EndedAt.After(maxEnd)) {
			maxEnd = a.EndedAt
		}

		ps, ok := byProv[a.Provider]
		if !ok {
			ps = &ProviderStats{Provider: a.Provider}
			byProv[a.Provider] = ps
		}
		ps.Runs++
		if a.Status == AgentFinished {
			ps.Succeeded++
		}
		if hasErrClass(a.Err, "quota_exhausted") {
			ps.QuotaErrors++
		}
		if a.Status == AgentTimeout || hasErrClass(a.Err, "timeout") {
			ps.Timeouts++
		}
		if hasErrClass(a.Err, "empty_output") {
			ps.EmptyOutputs++
		}
		ps.ActiveDuration += active
	}

	if !minStart.IsZero() && !maxEnd.IsZero() && maxEnd.After(minStart) {
		m.WallDuration = maxEnd.Sub(minStart)
	}

	if len(byProv) > 0 {
		m.ByProvider = make([]ProviderStats, 0, len(byProv))
		for _, ps := range byProv {
			m.ByProvider = append(m.ByProvider, *ps)
		}
		slices.SortFunc(m.ByProvider, func(a, b ProviderStats) int {
			return strings.Compare(a.Provider, b.Provider)
		})
	}

	return m
}

func agentActiveDuration(a *Agent) time.Duration {
	if a.ActiveDurationMS > 0 {
		return time.Duration(a.ActiveDurationMS) * time.Millisecond
	}
	if !a.StartedAt.IsZero() && !a.EndedAt.IsZero() && a.EndedAt.After(a.StartedAt) {
		return a.EndedAt.Sub(a.StartedAt)
	}
	return 0
}

func hasErrClass(errMsg, class string) bool {
	if errMsg == "" || class == "" {
		return false
	}
	if errMsg == class || strings.HasPrefix(errMsg, class+":") {
		return true
	}
	return strings.Contains(errMsg, class)
}
