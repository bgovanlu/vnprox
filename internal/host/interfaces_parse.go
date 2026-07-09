package host

import (
	"fmt"
	"strings"
)

// ParseError is returned by ParseInterfaces for a malformed input, with the
// 1-based source line number of the logical line that failed.
type ParseError struct {
	Msg  string
	Line int
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("host: interfaces(5) parse error at line %d: %s", e.Line, e.Msg)
}

// logicalLine is one interfaces(5) "line" after resolving backslash
// continuations: Text is the concatenation of every physical line's
// content with continuation backslashes and line terminators stripped
// (used for tokenizing), and Raw is the exact original bytes spanning all
// of its physical lines (used for lossless rendering). Start is the
// 1-based line number of the first physical line, for error reporting.
type logicalLine struct {
	Text  string
	Raw   string
	Start int
}

// splitLogicalLines splits data into logical lines, joining
// backslash-continued physical lines per interfaces(5) ("a line may be
// extended across multiple lines by making the last character a
// backslash"). It returns a ParseError if the file ends mid-continuation.
func splitLogicalLines(data []byte) ([]logicalLine, error) {
	s := string(data)
	var out []logicalLine
	lineNo := 0
	i := 0
	for i < len(s) {
		lineNo++
		start := lineNo
		var rawB strings.Builder
		var textB strings.Builder
		for {
			// Find the end of the current physical line.
			nl := strings.IndexByte(s[i:], '\n')
			var phys string // physical line content, including trailing \n if present
			if nl == -1 {
				phys = s[i:]
				i = len(s)
			} else {
				phys = s[i : i+nl+1]
				i += nl + 1
			}
			rawB.WriteString(phys)

			// content without the line terminator, for continuation
			// detection and tokenizing. A trailing \r (CRLF files) is
			// stripped for tokenizing purposes only — Raw always keeps
			// the original bytes.
			content := strings.TrimSuffix(strings.TrimSuffix(phys, "\n"), "\r")

			if strings.HasSuffix(content, "\\") {
				// Continuation: strip the trailing backslash and join
				// directly with the next physical line's content (no
				// separator inserted — matches ifupdown's behavior).
				textB.WriteString(content[:len(content)-1])
				if i >= len(s) {
					// Nothing follows this backslash (end of file, with
					// or without a trailing newline): there is no line
					// left to continue onto.
					return nil, &ParseError{Line: lineNo, Msg: "unterminated line continuation: backslash at end of file"}
				}
				lineNo++
				continue
			}
			// Not a continuation: this logical line is complete.
			textB.WriteString(content)
			break
		}
		out = append(out, logicalLine{Start: start, Text: textB.String(), Raw: rawB.String()})
	}
	return out, nil
}

// reserved top-level keywords (see interfaces_ast.go's package comment).
const (
	kwAuto            = "auto"
	kwNoAutoDown      = "no-auto-down"
	kwNoScripts       = "no-scripts"
	kwRename          = "rename"
	kwSource          = "source"
	kwSourceDirectory = "source-directory"
	kwMapping         = "mapping"
	kwIface           = "iface"
	allowPrefix       = "allow-"
	kwInherits        = "inherits"
)

// ParseInterfaces parses the literal content of an /etc/network/interfaces
// file (ifupdown2 stanza syntax) into a lossless File AST. See
// interfaces_ast.go for the AST shape and the round-trip guarantee.
func ParseInterfaces(data []byte) (*File, error) {
	lines, err := splitLogicalLines(data)
	if err != nil {
		return nil, err
	}

	f := &File{}
	var open *Entry // the currently-open iface/mapping stanza, if any

	closeOpen := func() {
		if open != nil {
			f.Entries = append(f.Entries, *open)
			open = nil
		}
	}

	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln.Text)

		if trimmed == "" {
			if open != nil {
				open.Body = append(open.Body, BodyItem{Kind: BodyBlank, Raw: ln.Raw})
			} else {
				f.Entries = append(f.Entries, Entry{Kind: KindBlank, Raw: ln.Raw})
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if open != nil {
				open.Body = append(open.Body, BodyItem{Kind: BodyComment, Raw: ln.Raw})
			} else {
				f.Entries = append(f.Entries, Entry{Kind: KindComment, Raw: ln.Raw})
			}
			continue
		}

		tokens := strings.Fields(trimmed)
		kw := tokens[0]
		args := tokens[1:]

		switch {
		case kw == kwAuto:
			closeOpen()
			f.Entries = append(f.Entries, Entry{Kind: KindAuto, Raw: ln.Raw, Ifaces: args})
		case strings.HasPrefix(kw, allowPrefix) && len(kw) > len(allowPrefix):
			closeOpen()
			f.Entries = append(f.Entries, Entry{
				Kind: KindAllow, Raw: ln.Raw,
				Class: kw[len(allowPrefix):], Ifaces: args,
			})
		case kw == kwNoAutoDown:
			closeOpen()
			f.Entries = append(f.Entries, Entry{Kind: KindNoAutoDown, Raw: ln.Raw, Ifaces: args})
		case kw == kwNoScripts:
			closeOpen()
			f.Entries = append(f.Entries, Entry{Kind: KindNoScripts, Raw: ln.Raw, Ifaces: args})
		case kw == kwRename:
			closeOpen()
			f.Entries = append(f.Entries, Entry{Kind: KindRename, Raw: ln.Raw, Renames: args})
		case kw == kwSource:
			closeOpen()
			f.Entries = append(f.Entries, Entry{Kind: KindSource, Raw: ln.Raw, Path: strings.Join(args, " ")})
		case kw == kwSourceDirectory:
			closeOpen()
			f.Entries = append(f.Entries, Entry{Kind: KindSourceDirectory, Raw: ln.Raw, Path: strings.Join(args, " ")})
		case kw == kwMapping:
			closeOpen()
			open = &Entry{Kind: KindMapping, Raw: ln.Raw, Pattern: strings.Join(args, " ")}
		case kw == kwIface:
			closeOpen()
			if len(args) < 3 {
				return nil, &ParseError{
					Line: ln.Start,
					Msg:  fmt.Sprintf("iface stanza requires name, family and method (got %d argument(s))", len(args)),
				}
			}
			e := Entry{Kind: KindIface, Raw: ln.Raw, Name: args[0], Family: args[1], Method: args[2]}
			if len(args) > 3 && args[3] == kwInherits {
				e.Inherits = args[4:]
			}
			open = &e
		default:
			// Not a reserved keyword: only valid as an option line
			// inside an already-open iface/mapping stanza.
			if open == nil {
				return nil, &ParseError{
					Line: ln.Start,
					Msg:  fmt.Sprintf("unexpected line %q outside any iface/mapping stanza", trimmed),
				}
			}
			open.Body = append(open.Body, BodyItem{
				Kind: BodyOption, Raw: ln.Raw,
				Key: kw, Value: strings.Join(args, " "),
			})
		}
	}
	closeOpen()

	return f, nil
}
