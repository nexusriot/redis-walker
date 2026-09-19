// Package format recognises how a Redis string value is encoded and renders it
// in a readable form. Decoding never changes what is stored: the decoded text
// is only ever shown, and the editor always works on the raw value.
package format

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is the encoding of a value, or of one layer of it.
type Kind string

const (
	Empty  Kind = "empty"
	Text   Kind = "text"
	JSON   Kind = "json"
	XML    Kind = "xml"
	Gzip   Kind = "gzip"
	Zlib   Kind = "zlib"
	Base64 Kind = "base64"
	Binary Kind = "binary"
)

// maxDecoded bounds how much data a decoding layer may produce, so that a
// compressed value cannot exhaust memory when it is expanded for display.
const maxDecoded = 8 << 20

// maxDepth bounds how many nested layers are unwrapped.
const maxDepth = 4

// Result describes one value.
type Result struct {
	// Chain lists the layers that were unwrapped, outermost first.
	Chain []Kind
	// Kind is the encoding of the innermost payload.
	Kind Kind
	// Decoded is the readable rendering of the value.
	Decoded string
	// Changed reports whether Decoded differs from the raw value.
	Changed bool
	// Printable reports whether the raw value can be displayed as text.
	Printable bool
}

// Describe renders the decoding chain, e.g. "base64 -> gzip -> json".
func (r Result) Describe() string {
	parts := make([]string, 0, len(r.Chain)+1)
	for _, k := range r.Chain {
		parts = append(parts, string(k))
	}
	parts = append(parts, string(r.Kind))
	return strings.Join(parts, " -> ")
}

// Detect inspects a value and returns its readable form.
func Detect(raw string) Result {
	r := Result{Printable: printable(raw)}
	if raw == "" {
		r.Kind = Empty
		return r
	}
	kind, decoded, chain := decode(raw, 0)
	r.Kind, r.Decoded, r.Chain = kind, decoded, chain
	r.Changed = decoded != raw
	return r
}

func decode(value string, depth int) (Kind, string, []Kind) {
	if depth >= maxDepth {
		return leafKind(value), value, nil
	}

	if inner, ok := gunzip(value); ok {
		kind, decoded, chain := decode(inner, depth+1)
		return kind, decoded, append([]Kind{Gzip}, chain...)
	}
	if inner, ok := inflate(value); ok {
		kind, decoded, chain := decode(inner, depth+1)
		return kind, decoded, append([]Kind{Zlib}, chain...)
	}
	if inner, ok := unbase64(value); ok {
		kind, decoded, chain := decode(inner, depth+1)
		return kind, decoded, append([]Kind{Base64}, chain...)
	}
	if pretty, ok := prettyJSON(value); ok {
		return JSON, pretty, nil
	}
	if pretty, ok := prettyXML(value); ok {
		return XML, pretty, nil
	}
	return leafKind(value), value, nil
}

func leafKind(value string) Kind {
	if value == "" {
		return Empty
	}
	if !printable(value) {
		return Binary
	}
	return Text
}

// printable reports whether a value is valid UTF-8 without control characters
// other than the usual whitespace.
func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func limitedRead(r io.Reader) (string, bool) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, maxDecoded+1))
	if err != nil || n > maxDecoded || n == 0 {
		return "", false
	}
	return buf.String(), true
}

func gunzip(value string) (string, bool) {
	if len(value) < 3 || value[0] != 0x1f || value[1] != 0x8b {
		return "", false
	}
	zr, err := gzip.NewReader(strings.NewReader(value))
	if err != nil {
		return "", false
	}
	defer zr.Close()
	return limitedRead(zr)
}

func inflate(value string) (string, bool) {
	if len(value) < 2 || value[0] != 0x78 {
		return "", false
	}
	zr, err := zlib.NewReader(strings.NewReader(value))
	if err != nil {
		return "", false
	}
	defer zr.Close()
	return limitedRead(zr)
}

// unbase64 only reports success when the decoded bytes look like something
// worth showing, so that ordinary words are not mistaken for base64.
func unbase64(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if len(v) < 16 || len(v)%4 != 0 || !isBase64Alphabet(v) {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	s := string(decoded)
	if len(s) >= 2 && s[0] == 0x1f && byte(s[1]) == 0x8b {
		return s, true
	}
	if !printable(s) {
		return "", false
	}
	if _, ok := prettyJSON(s); ok {
		return s, true
	}
	if strings.ContainsAny(s, " {}[]:,/\\-_.") || len(strings.Fields(s)) > 1 {
		return s, true
	}
	return "", false
}

func isBase64Alphabet(s string) bool {
	trimmed := strings.TrimRight(s, "=")
	if len(trimmed) == 0 || len(s)-len(trimmed) > 2 {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
		default:
			return false
		}
	}
	return true
}

func prettyJSON(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 {
		return "", false
	}
	switch trimmed[0] {
	case '{', '[':
	default:
		return "", false
	}
	if !json.Valid([]byte(trimmed)) {
		return "", false
	}
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(trimmed), "", "  "); err != nil {
		return "", false
	}
	return out.String(), true
}

// PrettyJSON re-indents a JSON document.
func PrettyJSON(value string) (string, error) {
	out, ok := prettyJSON(value)
	if !ok {
		return "", fmt.Errorf("not a JSON document")
	}
	return out, nil
}

// ValidateJSON reports why a document is not valid JSON.
func ValidateJSON(value string) error {
	var v interface{}
	return json.Unmarshal([]byte(value), &v)
}

// LooksLikeJSON reports whether a value is a JSON object or array.
func LooksLikeJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	return len(trimmed) > 1 && (trimmed[0] == '{' || trimmed[0] == '[')
}

func prettyXML(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 3 || trimmed[0] != '<' {
		return "", false
	}
	dec := xml.NewDecoder(strings.NewReader(trimmed))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	enc.Indent("", "  ")
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false
		}
		if cd, ok := tok.(xml.CharData); ok && len(strings.TrimSpace(string(cd))) == 0 {
			continue
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", false
		}
	}
	if err := enc.Flush(); err != nil {
		return "", false
	}
	if out.Len() == 0 {
		return "", false
	}
	return out.String(), true
}

// HexDump renders a value as offset, hex bytes and printable ASCII.
func HexDump(raw string, width int) string {
	if width <= 0 {
		width = 16
	}
	var b strings.Builder
	data := []byte(raw)
	for off := 0; off < len(data); off += width {
		end := off + width
		if end > len(data) {
			end = len(data)
		}
		chunk := data[off:end]
		fmt.Fprintf(&b, "%08x  ", off)
		for i := 0; i < width; i++ {
			if i < len(chunk) {
				fmt.Fprintf(&b, "%02x ", chunk[i])
			} else {
				b.WriteString("   ")
			}
			if i%8 == 7 {
				b.WriteByte(' ')
			}
		}
		b.WriteString("|")
		for _, c := range chunk {
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	if b.Len() == 0 {
		return "(empty)\n"
	}
	return b.String()
}
