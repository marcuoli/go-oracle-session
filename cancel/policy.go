// Package cancel owns joined cancellation and physical-session health evidence.
package cancel

import "time"

// Health records whether the evidence proves a connection reusable.
type Health uint8

// Unknown, Reusable and Unusable retain the distinction between missing proof
// and a conclusively discarded transport.
const (
	Unknown Health = iota
	Reusable
	Unusable
)

// Evidence separates completed workers from protocol and transaction health.
// A returned call alone does not establish safe connection reuse.
type Evidence struct {
	OperationJoined, RowsJoined, BreakJoined, CleanupJoined bool
	ProtocolSettled, TransactionUsable, TransportDiscarded  bool
}

// Classify requires affirmative evidence for every reuse condition.
func Classify(e Evidence) Health {
	if e.TransportDiscarded {
		return Unusable
	}
	if e.OperationJoined && e.RowsJoined && e.BreakJoined && e.CleanupJoined && e.ProtocolSettled && e.TransactionUsable {
		return Reusable
	}
	return Unknown
}

// Cutoff selects the earlier query-grace or parent-interruption boundary.
// Callers retain deadline provenance rather than inferring it from error text.
func Cutoff(queryAt, parentAt time.Time, queryGrace, parentGrace time.Duration) time.Time {
	cutoff := queryAt.Add(queryGrace)
	if !parentAt.IsZero() {
		parentCutoff := parentAt.Add(parentGrace)
		if queryAt.IsZero() || parentCutoff.Before(cutoff) {
			return parentCutoff
		}
	}
	return cutoff
}
