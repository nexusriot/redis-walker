package format

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"strings"
	"testing"
)

func gzipOf(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func zlibOf(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestDetectPlainText(t *testing.T) {
	r := Detect("hello world")
	if r.Kind != Text || r.Changed || !r.Printable {
		t.Fatalf("result = %+v", r)
	}
}

func TestDetectEmpty(t *testing.T) {
	if r := Detect(""); r.Kind != Empty {
		t.Fatalf("result = %+v", r)
	}
}

func TestDetectJSONIsPrettyPrinted(t *testing.T) {
	r := Detect(`{"b":1,"a":[1,2]}`)
	if r.Kind != JSON {
		t.Fatalf("kind = %v", r.Kind)
	}
	if !strings.Contains(r.Decoded, "\n") || !r.Changed {
		t.Fatalf("the document was not re-indented: %q", r.Decoded)
	}
	if !strings.Contains(r.Decoded, `"a"`) {
		t.Fatalf("decoded = %q", r.Decoded)
	}
}

func TestDetectInvalidJSONStaysText(t *testing.T) {
	if r := Detect(`{"broken": `); r.Kind != Text {
		t.Fatalf("kind = %v", r.Kind)
	}
}

func TestDetectXML(t *testing.T) {
	r := Detect(`<root><child a="1">x</child></root>`)
	if r.Kind != XML {
		t.Fatalf("kind = %v", r.Kind)
	}
	if !strings.Contains(r.Decoded, "\n") {
		t.Fatalf("the document was not indented: %q", r.Decoded)
	}
}

func TestDetectGzipChain(t *testing.T) {
	r := Detect(gzipOf(t, `{"a":1}`))
	if r.Kind != JSON {
		t.Fatalf("kind = %v", r.Kind)
	}
	if len(r.Chain) != 1 || r.Chain[0] != Gzip {
		t.Fatalf("chain = %v", r.Chain)
	}
	if r.Describe() != "gzip -> json" {
		t.Fatalf("describe = %q", r.Describe())
	}
	if r.Printable {
		t.Fatal("gzip data is not printable")
	}
}

func TestDetectZlib(t *testing.T) {
	r := Detect(zlibOf(t, "plain payload text"))
	if r.Kind != Text || len(r.Chain) != 1 || r.Chain[0] != Zlib {
		t.Fatalf("result = %+v", r)
	}
	if r.Decoded != "plain payload text" {
		t.Fatalf("decoded = %q", r.Decoded)
	}
}

func TestDetectBase64OfJSON(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte(`{"hello":"world"}`))
	r := Detect(raw)
	if r.Kind != JSON || len(r.Chain) != 1 || r.Chain[0] != Base64 {
		t.Fatalf("result = %+v (%v)", r, r.Chain)
	}
}

func TestDetectBase64OfGzipOfJSON(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte(gzipOf(t, `{"a":[1,2,3]}`)))
	r := Detect(raw)
	if r.Describe() != "base64 -> gzip -> json" {
		t.Fatalf("describe = %q", r.Describe())
	}
}

// A word that happens to use only base64 characters must not be decoded.
func TestDetectDoesNotMisreadOrdinaryText(t *testing.T) {
	for _, in := range []string{"session", "abcdefghabcdefgh", "12345678", "0123456789abcdef"} {
		if r := Detect(in); len(r.Chain) != 0 {
			t.Errorf("Detect(%q) decoded as %v", in, r.Chain)
		}
	}
}

func TestDetectBinary(t *testing.T) {
	r := Detect("\x00\x01\x02binary\xff")
	if r.Kind != Binary || r.Printable {
		t.Fatalf("result = %+v", r)
	}
}

func TestPrettyAndValidateJSON(t *testing.T) {
	if _, err := PrettyJSON(`{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err := PrettyJSON(`not json`); err == nil {
		t.Fatal("expected an error")
	}
	if err := ValidateJSON(`{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSON(`{"a":}`); err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestLooksLikeJSON(t *testing.T) {
	if !LooksLikeJSON(`  {"a":1}`) || !LooksLikeJSON(`[1]`) {
		t.Fatal("JSON documents not recognised")
	}
	if LooksLikeJSON("plain") || LooksLikeJSON("") {
		t.Fatal("plain text recognised as JSON")
	}
}

func TestHexDump(t *testing.T) {
	out := HexDump("AB\x00", 16)
	if !strings.HasPrefix(out, "00000000  41 42 00 ") {
		t.Fatalf("hex dump = %q", out)
	}
	if !strings.Contains(out, "|AB.|") {
		t.Fatalf("ascii column missing: %q", out)
	}
	if lines := strings.Count(HexDump(strings.Repeat("x", 33), 16), "\n"); lines != 3 {
		t.Fatalf("33 bytes at width 16 produced %d lines", lines)
	}
	if HexDump("", 16) != "(empty)\n" {
		t.Fatalf("empty dump = %q", HexDump("", 16))
	}
}

// A compressed value that expands beyond the display limit must be refused
// rather than loaded into memory.
func TestDecodingIsBounded(t *testing.T) {
	huge := gzipOf(t, strings.Repeat("a", maxDecoded+10))
	r := Detect(huge)
	if len(r.Chain) != 0 {
		t.Fatalf("an oversized payload was expanded: %v", r.Chain)
	}
}
