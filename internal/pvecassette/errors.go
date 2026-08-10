package pvecassette

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bgovanlu/vnprox/internal/redact"
)

// Sentinel errors. Callers use errors.Is/errors.As rather than matching
// formatted messages.
var (
	// ErrCassetteInvalid indicates a cassette is missing something it
	// cannot be replayed (or trusted) without — a method, an absolute
	// path, a status, or the PVE version that produced it.
	ErrCassetteInvalid = errors.New("pvecassette: cassette invalid")

	// ErrSecretInCassette indicates a response body carries credential-
	// shaped material and therefore must not reach disk. It is returned by
	// Writer.Write (nothing is written) and by Load (a hand-edited
	// cassette gets the same scrutiny as a recorded one).
	ErrSecretInCassette = errors.New("pvecassette: response contains a secret")

	// ErrDuplicateCassette indicates two cassette files in one directory
	// claim the same request. Which one wins would decide what a test
	// sees, so neither does: loading fails and names both files.
	ErrDuplicateCassette = errors.New("pvecassette: duplicate cassette")
)

// SecretError names exactly which field made a recording illegal.
//
// "Fails the write" is only useful if the operator can act on it, and the
// action is always specific to a field: stop recording the login endpoint,
// exclude the node whose SDN zone holds a NetBox token, or file the fact
// that PVE echoes a credential where we did not expect one. An error that
// said only "secret detected" would send them looking through a body they
// are not supposed to read.
type SecretError struct {
	Method   string
	Path     string
	Findings []redact.Finding
}

// maxNamedFindings bounds how many fields one error names. A response that
// is nothing but credentials should not produce a screenful; the first few
// are enough to act on and the count says how many more there were.
const maxNamedFindings = 5

func (e *SecretError) Error() string {
	named := e.Findings
	suffix := ""
	if len(named) > maxNamedFindings {
		named = named[:maxNamedFindings]
		suffix = fmt.Sprintf(" (and %d more)", len(e.Findings)-maxNamedFindings)
	}
	parts := make([]string, 0, len(named))
	for _, f := range named {
		parts = append(parts, f.String())
	}
	return fmt.Sprintf("pvecassette: refusing to record %s %s: response body %s%s",
		e.Method, e.Path, strings.Join(parts, "; "), suffix)
}

// Unwrap makes errors.Is(err, ErrSecretInCassette) work.
func (e *SecretError) Unwrap() error { return ErrSecretInCassette }

// Fields returns just the field paths that fired, in report order — the
// shape a test asserts on.
func (e *SecretError) Fields() []string {
	out := make([]string, 0, len(e.Findings))
	for _, f := range e.Findings {
		out = append(out, f.Field)
	}
	return out
}
