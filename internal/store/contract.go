package store

import "fmt"

// Store contract version — the write-safety handshake.
//
// Ghost's guarantees (locked read-only bit, pinned lifecycle immunity,
// triage) are enforced in the LIBRARY, so a stale binary opening a newer DB
// silently bypasses every guarantee it predates. Live near-miss (2026-07-14):
// a pre-lock CLI on PATH overwrote a locked identity key. The DB records the
// contract version it was written under (PRAGMA user_version); a binary
// whose supported version is OLDER refuses writes — reads stay allowed so
// old tooling can still inspect.
//
// Bump this when a release adds a guarantee that older binaries would
// violate by writing:
//
//	1 — protected-memory contract (locked tag, pinned lifecycle immunity),
//	    write-time triage, tag-op gating.
const storeContractVersion = 1

// checkContract stamps fresh/older DBs up to this binary's version and
// records read-only mode when the DB is newer than the binary.
func (s *SQLiteStore) checkContract() {
	var uv int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil {
		return
	}
	if uv > storeContractVersion {
		s.contractReadOnly = true
		return
	}
	if uv < storeContractVersion {
		s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, storeContractVersion))
	}
}

// errContractNewer is returned for writes against a DB whose contract this
// binary predates.
func (s *SQLiteStore) errContractNewer() error {
	return fmt.Errorf("this ghost binary supports store contract v%d but the database was written under a newer contract: writes refused (upgrade the binary; reads still work)", storeContractVersion)
}
