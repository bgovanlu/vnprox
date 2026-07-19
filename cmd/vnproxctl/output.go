package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// outputFlagUsage is the shared `-o` flag description every command in this
// binary registers (T-1105 acceptance criterion 4: "-o json on EVERY
// command"). Kept as one string constant so every flag.FlagSet's help text
// reads identically.
const outputFlagUsage = "output format: table (default, human-readable) or json"

// defaultOutputFormat is what every command's `-o` flag defaults to absent
// the flag: unchanged human-readable text, so retrofitting `-o` onto
// `status`/`snapshots`/`rollback-now` cannot alter their existing output by
// itself (T-1105's regression requirement).
const defaultOutputFormat = "table"

// parseOutputFormat validates a `-o` flag's value. Only "table" and "json"
// are recognized; anything else is a usage error (ExitUsage) rather than
// silently falling back to table, so a typo'd `-o jsno` fails loudly instead
// of producing unexpected text output in a script.
func parseOutputFormat(raw string) (jsonOutput bool, err error) {
	switch raw {
	case "", defaultOutputFormat:
		return false, nil
	case "json":
		return true, nil
	default:
		return false, fmt.Errorf("unrecognized -o value %q (want %q or %q)", raw, "table", "json")
	}
}

// writeJSONOut encodes v as indented JSON to w — the `-o json` rendering
// every remote/apply command shares. Errors are surfaced to the caller
// (rather than swallowed) since a broken encode means the command's JSON
// contract can't be honored; callers treat it as ExitError.
func writeJSONOut(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
