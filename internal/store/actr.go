package store

import (
	"math"
	"os"
	"strconv"
	"time"

	"github.com/rcliao/ghost/internal/model"
)

// ACT-R base-level activation (opt-in via GHOST_ACTR=1).
//
// Replaces the separate recency (7-day exp decay) and access-frequency (log
// scale) terms in computeContextScore with one principled scalar from the
// ACT-R cognitive architecture: A(m) = ln(Σ tⱼ^-d) over the memory's access
// history, d = 0.5. Recency and frequency stop being independently tuned
// weights and become emergent properties of one decay law.
//
// Ghost keeps no per-access log — only access_count n, created_at, and
// last_accessed_at — so we use the standard uniform-spacing approximation
// (Petrov 2006): the most recent access contributes exactly, and the remaining
// n−1 accesses are integrated as if spread evenly over the memory's lifetime:
//
//	A ≈ ln( t_last^-d  +  (n−1)·(t_life^(1−d) − t_last^(1−d)) / ((1−d)·(t_life − t_last)) )
//
// Activation is mapped to [0,1] via the ACT-R retrieval-probability sigmoid
// P = 1/(1+exp(−(A−τ)/s)) with τ = 0, s = 1, so it can slot into the existing
// weighted sum, taking over the combined recency+access weight for the kind.
//
// Measured (2026-07-12): personal eval REGRESSES under every swept config
// (best meanMRR 0.734 / R@5 0.750 across τ∈{-3..0}, s∈{1,2} vs baseline
// 0.790 / 0.833) — the hand-tuned exp-decay + log-frequency pair outperforms
// the three-point ACT-R approximation here. Keep off by default; revisit only
// with a real per-access log, where the approximation stops being the weak link.
func actrEnabled() bool { return envBool("GHOST_ACTR") }

const (
	actrDecay = 0.5 // d — ACT-R canonical decay exponent
)

// actrThreshold (τ) and actrNoiseS (s) shape the activation→probability
// sigmoid. The theory fixes the decay law; these mapping constants are
// empirical, so they're env-tunable for eval sweeps (GHOST_ACTR_TAU,
// GHOST_ACTR_S).
func actrThreshold() float64 { return envFloatDefault("GHOST_ACTR_TAU", 0.0) }
func actrNoiseS() float64    { return envFloatDefault("GHOST_ACTR_S", 1.0) }

func envFloatDefault(name string, def float64) float64 {
	if env := os.Getenv(name); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil {
			return v
		}
	}
	return def
}

// actrActivationExact computes activation from real logged access timestamps
// (memory_accesses table) instead of the three-point approximation:
// A = ln(t_created^-d + Σ t_j^-d) — creation counts as the first presentation.
// This is the path the approximation's measured regression points at: with a
// real access log, recency and frequency come from the actual history.
func actrActivationExact(created time.Time, accesses []time.Time, now time.Time) float64 {
	const minAge = 1.0 / 60.0
	d := actrDecay

	age := func(t time.Time) float64 {
		h := now.Sub(t).Hours()
		if h < minAge {
			h = minAge
		}
		return h
	}

	sum := math.Pow(age(created), -d)
	for _, t := range accesses {
		sum += math.Pow(age(t), -d)
	}
	activation := math.Log(sum)
	return 1.0 / (1.0 + math.Exp(-(activation-actrThreshold())/actrNoiseS()))
}

// actrActivation returns the retrieval probability [0,1] for a memory from
// its access statistics at time `now`.
func actrActivation(m model.Memory, now time.Time) float64 {
	// Times in hours, floored to a minimum so brand-new memories don't blow
	// up t^-d (an access "right now" is at least ~1 minute old).
	const minAge = 1.0 / 60.0

	tLife := now.Sub(m.CreatedAt).Hours()
	if tLife < minAge {
		tLife = minAge
	}
	tLast := tLife
	if m.LastAccessedAt != nil && !m.LastAccessedAt.IsZero() {
		tLast = now.Sub(*m.LastAccessedAt).Hours()
		if tLast < minAge {
			tLast = minAge
		}
		if tLast > tLife {
			tLast = tLife
		}
	}

	n := float64(m.AccessCount)
	if n < 1 {
		// Never retrieved: creation is the only "presentation".
		n = 1
		tLast = tLife
	}

	d := actrDecay
	sum := math.Pow(tLast, -d)
	if n > 1 && tLife > tLast {
		// Integral approximation for the other n−1 accesses spread uniformly
		// across (tLast, tLife].
		sum += (n - 1) * (math.Pow(tLife, 1-d) - math.Pow(tLast, 1-d)) / ((1 - d) * (tLife - tLast))
	} else if n > 1 {
		// Degenerate: all mass at tLast.
		sum += (n - 1) * math.Pow(tLast, -d)
	}

	activation := math.Log(sum)
	return 1.0 / (1.0 + math.Exp(-(activation-actrThreshold())/actrNoiseS()))
}
