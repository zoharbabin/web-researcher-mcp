package audit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrChainBroken is returned (wrapped) by VerifyChain when a recorded Hash
// does not match the hash recomputed from its PrevHash + canonical event
// bytes, or a PrevHash does not match the previous line's recorded Hash —
// either signals a tampered, edited, or reordered log line (#466 half 2).
var ErrChainBroken = errors.New("audit: hash chain broken")

// hashEvent computes the tamper-evidence hash for one event: SHA-256 of
// (prevHash + canonical JSON bytes of the event with PrevHash set to prevHash
// and Hash cleared). It never mutates the caller's event — a local copy is
// hashed — and never retains prevHash beyond this single call, so chaining
// N events costs O(1) memory regardless of N (#466 half 2, milestone #555 §4.1).
func hashEvent(prevHash string, event AuditEvent) (string, error) {
	event.PrevHash = prevHash
	event.Hash = ""
	data, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("audit: marshal event for hashing: %w", err)
	}
	sum := sha256.Sum256(append([]byte(prevHash), data...))
	return hex.EncodeToString(sum[:]), nil
}

// lastEventHash returns the recorded Hash of the last non-blank line in the
// audit log at path, so a Logger restarting against a pre-existing file can
// seed its in-memory prevHash correctly instead of always starting from ""
// (which would make VerifyChain misreport tampering at every restart
// boundary). Returns "" on any read/parse error or an empty file — the chain
// simply starts fresh in that case, matching prior behavior.
func lastEventHash(path string) string {
	f, err := os.Open(path) // #nosec G304 -- operator-configured audit output path, not user input
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	last := ""
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var event AuditEvent
		if json.Unmarshal(raw, &event) != nil {
			continue
		}
		last = event.Hash
	}
	return last
}

// VerifyChain walks a JSONL audit log (one AuditEvent per line, as written by
// Logger) and confirms every line's Hash matches the hash recomputed from its
// PrevHash and its own bytes, and that every line's PrevHash matches the
// previous line's recorded Hash. It returns nil if the chain is intact, or an
// error wrapping ErrChainBroken naming the first broken line otherwise.
//
// The first line's PrevHash is trusted as a chain anchor and not compared
// against anything in this reader — it may legitimately point to the last
// event of a rotated predecessor file that isn't part of this input — but its
// own Hash is still verified against its own PrevHash + bytes, so tampering
// with the first line's content is still caught.
//
// A log written before this feature shipped (no Hash/PrevHash fields) is
// correctly reported as broken at line 1 — there is no chain to verify, only
// events to protect going forward.
func VerifyChain(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024) // audit lines carry arbitrary Metadata; allow generous line size
	prevHash := ""
	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var event AuditEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return fmt.Errorf("%w: line %d: invalid JSON: %v", ErrChainBroken, line, err)
		}
		if line > 1 && event.PrevHash != prevHash {
			return fmt.Errorf("%w: line %d (event_type=%q): prev_hash %q does not match previous line's hash %q",
				ErrChainBroken, line, event.EventType, event.PrevHash, prevHash)
		}
		expected, err := hashEvent(event.PrevHash, event)
		if err != nil {
			return fmt.Errorf("audit: line %d: %w", line, err)
		}
		if event.Hash != expected {
			return fmt.Errorf("%w: line %d (event_type=%q): recorded hash %q does not match recomputed hash %q",
				ErrChainBroken, line, event.EventType, event.Hash, expected)
		}
		prevHash = event.Hash
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("audit: reading chain: %w", err)
	}
	return nil
}
