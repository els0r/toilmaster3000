package hook

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A load that fails only at CLOSE is a failed load, not an empty one. Both
// stores are read once at construction and never again, so a truncated read
// that reports itself on the way out is the difference between booting with the
// real ledger and booting with a silently short one — a verdict store missing
// its trailing rows would re-run screens whose verdicts are already on disk, and
// a fired ledger missing rows would re-fire notifiers that already posted.
//
// The APPEND side has no twin here: internal/jsonl owns the write path for
// every .state writer and asserts its own close behaviour (a row that fails at
// flush is a failed append), so a copy at this layer would only pin the same
// property twice.
var errClose = errors.New("close failed")

// closeFailRead is the read-close fake: a file that hands back its contents and
// then fails on the way out, which no temp directory can be made to do.
type closeFailRead struct {
	io.Reader
}

func (closeFailRead) Close() error { return errClose }

func TestFiredLedgerLoadReturnsCloseError(t *testing.T) {
	l := &FiredLedger{
		path:  "hookfires.jsonl",
		fired: map[FireKey]bool{},
		openRead: func(string) (io.ReadCloser, error) {
			return closeFailRead{Reader: strings.NewReader("")}, nil
		},
	}

	err := l.load()

	require.ErrorIs(t, err, errClose)
}

func TestVerdictStoreLoadReturnsCloseError(t *testing.T) {
	s := &VerdictStore{
		path:        "verdicts.jsonl",
		latest:      map[VerdictKey]VerdictRecord{},
		errorStreak: map[VerdictKey]int{},
		openRead: func(string) (io.ReadCloser, error) {
			return closeFailRead{Reader: strings.NewReader("")}, nil
		},
	}

	err := s.load()

	require.ErrorIs(t, err, errClose)
}
