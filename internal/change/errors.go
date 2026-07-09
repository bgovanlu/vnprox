package change

import "fmt"

// OpDecodeError is returned by Op.UnmarshalJSON (and, transitively, by
// decoding a []Op request body) when the JSON does not match the
// documented v1 op vocabulary (docs/data-model.md §3): an unrecognized
// "op" type, a missing target where one is required, a malformed target
// ref, or an unknown field anywhere in the envelope or its params. Path
// pinpoints the offending field (e.g. "op", "target", "params.mtuu") so
// docs/api.md's `validation_failed` error envelope's `details` can surface
// it to the caller without the API layer re-deriving it from a plain error
// string.
type OpDecodeError struct {
	Path    string
	Message string
}

func (e *OpDecodeError) Error() string {
	return fmt.Sprintf("change: decoding op: %s: %s", e.Path, e.Message)
}

// ErrIllegalTransition is returned when a caller attempts a changeset
// status transition the state machine (changeset.go) does not allow, or
// attempts a draft-only mutation (UpdateDraft/Discard) on a changeset that
// is no longer editable.
type ErrIllegalTransition struct {
	From Status
	To   Status
}

func (e *ErrIllegalTransition) Error() string {
	return fmt.Sprintf("change: illegal changeset status transition %s -> %s", e.From, e.To)
}
