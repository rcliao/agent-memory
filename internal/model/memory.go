// Package model defines the core memory data types.
package model

import "time"

// Memory represents a stored memory entry.
type Memory struct {
	ID             string     `json:"id"`
	NS             string     `json:"ns"`
	Key            string     `json:"key"`
	Content        string     `json:"content"`
	Kind           string     `json:"kind"`
	Tags           []string   `json:"tags,omitempty"`
	Version        int        `json:"version"`
	Supersedes     string     `json:"supersedes,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	Priority       string     `json:"priority"`
	AccessCount    int        `json:"access_count"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
	Meta           string     `json:"meta,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	ChunkCount     int        `json:"chunks,omitempty"`
}

// Chunk represents an internal text chunk of a memory.
type Chunk struct {
	ID        string `json:"id"`
	MemoryID  string `json:"memory_id"`
	Seq       int    `json:"seq"`
	Text      string `json:"text"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

// ValidKinds are the allowed memory kinds.
var ValidKinds = map[string]bool{
	"semantic":   true,
	"episodic":   true,
	"procedural": true,
}

// ValidPriorities are the allowed priority levels.
var ValidPriorities = map[string]bool{
	"low":      true,
	"normal":   true,
	"high":     true,
	"critical": true,
}
