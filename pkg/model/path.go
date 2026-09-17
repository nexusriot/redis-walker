package model

import "strings"

// redis-walker presents a flat Redis keyspace as a tree. The mapping between a
// real Redis key and the virtual path shown in the UI is:
//
//	key "a/b"     <-> path "/a/b"     (prefix "a/"  when used as a directory)
//	key "/a/b"    <-> path "/a/b"     (prefix "/a/" when used as a directory)
//	key "user:1"  <-> path "/user:1"
//
// A path is *only* used for display. Every mutation goes through Node.Key,
// which always holds the real Redis key, so keys that do not start with "/"
// are edited and deleted correctly.
//
// A "prefix" is the real key prefix of a directory: "" for the root, otherwise
// a key followed by a single "/".

// dirMarker is the leaf key written by MkDir so that an otherwise empty
// directory stays visible.
const dirMarker = ".dir"

// PathOf returns the virtual display path of a Redis key.
func PathOf(key string) string {
	if strings.HasPrefix(key, "/") {
		return key
	}
	return "/" + key
}

// PrefixOf returns the scan prefix for a directory key. The root ("" or "/")
// maps to the empty prefix, which matches the whole keyspace.
func PrefixOf(dirKey string) string {
	dirKey = strings.TrimSuffix(dirKey, "/")
	if dirKey == "" || dirKey == "/" {
		return ""
	}
	return dirKey + "/"
}

// ParentPrefix returns the scan prefix of the parent directory of prefix.
// The parent of the root is the root.
func ParentPrefix(prefix string) string {
	p := strings.TrimSuffix(prefix, "/")
	if p == "" {
		return ""
	}
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		// "a" or "/a": both live directly under the root.
		return ""
	}
	return p[:i] + "/"
}

// BaseOf returns the last path segment of a key.
func BaseOf(key string) string {
	k := strings.TrimSuffix(key, "/")
	i := strings.LastIndex(k, "/")
	if i < 0 {
		return k
	}
	return k[i+1:]
}

// DisplayDir renders a scan prefix as the path shown in the UI.
func DisplayDir(prefix string) string {
	p := strings.TrimSuffix(prefix, "/")
	if p == "" {
		return "/"
	}
	return PathOf(p)
}

// ChildKey joins a directory prefix and a single path segment into a real key.
func ChildKey(prefix, name string) string {
	return prefix + strings.TrimSuffix(name, "/")
}

// globEscape quotes the characters that Redis treats as glob meta characters in
// SCAN/KEYS MATCH patterns. Without this a key such as "cache[1]/x" would be
// read as a character class and SCAN would return (and DEL would remove!)
// a completely different set of keys.
func globEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\', '*', '?', '[', ']':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// splitChild splits a key that lives under prefix into its first path segment
// and whether anything follows it. ok is false when the key contributes no
// visible child (empty segment, or the key is the prefix itself).
//
// At the root the optional single leading "/" belongs to the child key, so that
// the keys "a/b" and "/a/b" stay distinct entries instead of being merged.
func splitChild(prefix, key string) (childKey, segment string, isDir, ok bool) {
	if !strings.HasPrefix(key, prefix) {
		return "", "", false, false
	}
	rest := key[len(prefix):]
	lead := ""
	if prefix == "" && strings.HasPrefix(rest, "/") {
		lead, rest = "/", rest[1:]
	}
	if rest == "" {
		return "", "", false, false
	}
	seg, _, hasTail := strings.Cut(rest, "/")
	if seg == "" {
		// Empty path segment ("a//b"); not representable as a tree node.
		return "", "", false, false
	}
	return prefix + lead + seg, seg, hasTail, true
}
