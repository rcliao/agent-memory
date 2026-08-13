package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Source identity: declared alias → canonical person-id mapping
// (docs/research/source-identity-design.md). Ghost never infers that two
// labels are the same person; equivalence is declared into this table and
// resolved by exact case-insensitive lookup. Memory rows are never rewritten:
// source_user stays verbatim forever, and resolution happens at read.

// SourceAlias is one declared spelling of a canonical source id.
type SourceAlias struct {
	NS        string `json:"ns"`
	Alias     string `json:"alias"`
	Canonical string `json:"canonical"`
	CreatedAt string `json:"created_at"`
}

// SetSourceAlias declares alias → canonical for a namespace. Chains are
// rejected in both directions so one person can never resolve to two ids:
// the canonical must not itself be a declared alias, and the alias string
// must not already be in use as some row's canonical.
func (s *SQLiteStore) SetSourceAlias(ctx context.Context, ns, alias, canonical string) error {
	alias = strings.TrimSpace(alias)
	canonical = strings.TrimSpace(canonical)
	if ns == "" || alias == "" || canonical == "" {
		return fmt.Errorf("source alias: ns, alias and canonical are all required")
	}
	if asciiFold(alias) == asciiFold(canonical) {
		return fmt.Errorf("source alias: alias %q and canonical %q are the same id", alias, canonical)
	}

	var target string
	err := s.db.QueryRowContext(ctx,
		`SELECT canonical FROM source_aliases WHERE ns = ? AND alias = ?`, ns, canonical).Scan(&target)
	if err == nil {
		return fmt.Errorf("source alias: canonical %q is itself a declared alias of %q — point at %q instead",
			canonical, target, target)
	}
	var chained string
	err = s.db.QueryRowContext(ctx,
		`SELECT alias FROM source_aliases WHERE ns = ? AND canonical = ? COLLATE NOCASE LIMIT 1`, ns, alias).Scan(&chained)
	if err == nil {
		return fmt.Errorf("source alias: %q is already the canonical of alias %q — aliasing it away would split one identity across two ids",
			alias, chained)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO source_aliases (ns, alias, canonical, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(ns, alias) DO UPDATE SET canonical = excluded.canonical`,
		ns, alias, canonical, time.Now().UTC().Format(time.RFC3339))
	return err
}

// ListSourceAliases returns all declared aliases for a namespace.
func (s *SQLiteStore) ListSourceAliases(ctx context.Context, ns string) ([]SourceAlias, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ns, alias, canonical, created_at FROM source_aliases WHERE ns = ? ORDER BY canonical, alias`, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceAlias
	for rows.Next() {
		var a SourceAlias
		if err := rows.Scan(&a.NS, &a.Alias, &a.Canonical, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteSourceAlias removes a declared alias (an "unmerge"). Memory rows are
// untouched; rows stored under the alias simply resolve to themselves again.
func (s *SQLiteStore) DeleteSourceAlias(ctx context.Context, ns, alias string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM source_aliases WHERE ns = ? AND alias = ?`, ns, alias)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("source alias: no alias %q in %s", alias, ns)
	}
	return nil
}

// sourceResolver resolves labels to canonical ids through the declared alias
// map of one namespace. Loaded once per operation that needs it.
type sourceResolver map[string]string // asciiFold(alias) → canonical

// loadSourceResolver reads the namespace's alias map. A missing or empty
// table yields an empty resolver, under which every label is its own
// canonical — exactly today's behaviour.
func (s *SQLiteStore) loadSourceResolver(ctx context.Context, ns string) sourceResolver {
	rows, err := s.db.QueryContext(ctx,
		`SELECT alias, canonical FROM source_aliases WHERE ns = ?`, ns)
	if err != nil {
		return sourceResolver{}
	}
	defer rows.Close()
	r := sourceResolver{}
	for rows.Next() {
		var alias, canonical string
		if err := rows.Scan(&alias, &canonical); err != nil {
			continue
		}
		r[asciiFold(alias)] = canonical
	}
	return r
}

// canonical resolves one label: the declared canonical if an alias row
// matches case-insensitively, else the label itself. One step, never chained
// (chains are rejected at declaration).
func (r sourceResolver) canonical(label string) string {
	if label == "" {
		return ""
	}
	if c, ok := r[asciiFold(label)]; ok {
		return c
	}
	return label
}

// sameSource reports whether two labels name the same canonical source.
// Empty labels never match anything — an unknown origin stays unknown.
func (r sourceResolver) sameSource(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return asciiFold(r.canonical(a)) == asciiFold(r.canonical(b))
}

// variants returns every stored spelling that resolves to the same canonical
// as the given label (including the label and the canonical themselves) —
// the IN-clause set for SQL-side filtering.
func (r sourceResolver) variants(label string) []string {
	c := r.canonical(label)
	out := []string{label}
	if asciiFold(c) != asciiFold(label) {
		out = append(out, c)
	}
	for alias, canonical := range r {
		if asciiFold(canonical) == asciiFold(c) && asciiFold(alias) != asciiFold(label) {
			out = append(out, alias)
		}
	}
	return out
}

// asciiFold lowercases ASCII letters only, matching SQLite's COLLATE NOCASE
// semantics: the observed fragmentation is Latin-script name variants, and
// CJK labels do not case-fold.
func asciiFold(s string) string {
	b := []byte(s)
	changed := false
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}
