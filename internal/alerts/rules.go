// Package alerts evaluates threshold rules without creating incidents or notifications.
package alerts

import (
	"fmt"
	"math"
	"time"
)

type Rule struct {
	Name          string  `json:"name"`
	Metric        string  `json:"metric"`
	Direction     string  `json:"direction"`
	FireAt        float64 `json:"fire_at"`
	RecoverAt     float64 `json:"recover_at"`
	WindowSeconds int     `json:"window_seconds"`
	MinSamples    int     `json:"min_samples"`
	MaxAgeSeconds int     `json:"max_age_seconds"`
	DiskPath      string  `json:"disk_path"`
}

type Observation struct {
	Average *float64
	Samples int64
	Latest  *time.Time
}

type Outcome struct {
	Firing     bool
	Status     string
	Transition string
}

func Validate(r Rule) error {
	if len(r.Name) < 1 || len(r.Name) > 80 ||
		r.WindowSeconds < 60 || r.WindowSeconds > 86400 ||
		r.MinSamples < 1 || r.MinSamples > 10000 ||
		r.MaxAgeSeconds < 1 || r.MaxAgeSeconds > r.WindowSeconds ||
		math.IsNaN(r.FireAt) || math.IsInf(r.FireAt, 0) ||
		math.IsNaN(r.RecoverAt) || math.IsInf(r.RecoverAt, 0) ||
		r.FireAt < 0 || r.FireAt > 100 || r.RecoverAt < 0 || r.RecoverAt > 100 {
		return fmt.Errorf("invalid rule name, window, sample count, age or percentage")
	}
	switch r.Metric {
	case "cpu", "memory":
		if r.DiskPath != "" {
			return fmt.Errorf("disk_path only applies to disk rules")
		}
	case "disk":
		if len(r.DiskPath) == 0 || len(r.DiskPath) > 1024 {
			return fmt.Errorf("disk rules require an exact disk_path")
		}
	default:
		return fmt.Errorf("unsupported metric")
	}
	switch r.Direction {
	case "above":
		if r.RecoverAt >= r.FireAt {
			return fmt.Errorf("above rule requires recover_at < fire_at")
		}
	case "below":
		if r.RecoverAt <= r.FireAt {
			return fmt.Errorf("below rule requires recover_at > fire_at")
		}
	default:
		return fmt.Errorf("unsupported direction")
	}
	return nil
}

// Evaluate holds the prior state if the window contains too few valid,
// recent measurements. Missing data is never interpreted as 0 or recovery.
func Evaluate(rule Rule, previousFiring bool, obs Observation, at time.Time) Outcome {
	out := Outcome{Firing: previousFiring, Status: "insufficient_data", Transition: "none"}
	if at.IsZero() || obs.Average == nil || obs.Latest == nil ||
		obs.Samples < int64(rule.MinSamples) ||
		math.IsNaN(*obs.Average) || math.IsInf(*obs.Average, 0) ||
		*obs.Average < 0 || *obs.Average > 100 ||
		obs.Latest.After(at) ||
		obs.Latest.Before(at.Add(-time.Duration(rule.MaxAgeSeconds)*time.Second)) {
		return out
	}

	firing := previousFiring
	if rule.Direction == "above" {
		if !firing && *obs.Average >= rule.FireAt {
			firing = true
		} else if firing && *obs.Average <= rule.RecoverAt {
			firing = false
		}
	} else if rule.Direction == "below" {
		if !firing && *obs.Average <= rule.FireAt {
			firing = true
		} else if firing && *obs.Average >= rule.RecoverAt {
			firing = false
		}
	}
	out.Firing = firing
	if firing {
		out.Status = "firing"
	} else {
		out.Status = "normal"
	}
	if firing && !previousFiring {
		out.Transition = "fired"
	} else if !firing && previousFiring {
		out.Transition = "recovered"
	}
	return out
}
