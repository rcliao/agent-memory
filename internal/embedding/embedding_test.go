package embedding

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     Vector
		expected float64
		delta    float64
	}{
		{"identical", Vector{1, 0, 0}, Vector{1, 0, 0}, 1.0, 0.001},
		{"orthogonal", Vector{1, 0, 0}, Vector{0, 1, 0}, 0.0, 0.001},
		{"opposite", Vector{1, 0, 0}, Vector{-1, 0, 0}, -1.0, 0.001},
		{"similar", Vector{1, 1, 0}, Vector{1, 0, 0}, 0.707, 0.01},
		{"empty", Vector{}, Vector{}, 0.0, 0.001},
		{"different lengths", Vector{1, 0}, Vector{1, 0, 0}, 0.0, 0.001},
		{"zero vector", Vector{0, 0, 0}, Vector{1, 0, 0}, 0.0, 0.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.expected) > tt.delta {
				t.Errorf("CosineSimilarity(%v, %v) = %f, want %f (±%f)", tt.a, tt.b, got, tt.expected, tt.delta)
			}
		})
	}
}

func TestNewFromEnv_Disabled(t *testing.T) {
	// With no env vars set, should return nil
	e := NewFromEnv()
	if e != nil {
		t.Error("expected nil embedder when no provider configured")
	}
}
