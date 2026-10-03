package patch

import (
	"strings"
	"unicode/utf8"
)

// escapeYAMLIncompatibleJSON rewrites characters that cannot survive a YAML 1.1
// parse when they appear raw inside JSON strings.
//
// patchStrategicMerge feeds JSON to kyaml, and kyaml parses it with go.yaml.in.
// The reader rejects U+007F, most of U+0080-U+009F, and U+FFFE/U+FFFF before
// any token is decoded (see go.yaml.in/yaml/v3 readerc.go). encoding/json emits
// those characters unescaped. yaml.MustParse turned that reader error into a
// panic, which net/http recovered by closing the admission connection.
//
// U+0085 is legal for the reader but is a line break inside a double-quoted
// scalar, so the scanner folds it into a space. Raw line breaks are escaped
// for the same reason. \uXXXX is valid in both JSON and YAML double-quoted
// strings, and the decoder restores the original character. Input that needs
// no rewrite is returned unchanged.
func escapeYAMLIncompatibleJSON(in string) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(in); {
		if !inString {
			if in[i] == '"' {
				inString = true
			}
			if b.Len() > 0 {
				b.WriteByte(in[i])
			}
			i++
			continue
		}

		switch in[i] {
		case '"':
			inString = false
			if b.Len() > 0 {
				b.WriteByte('"')
			}
			i++
		case '\\':
			n := jsonEscapeLen(in, i)
			if b.Len() > 0 {
				b.WriteString(in[i : i+n])
			}
			i += n
		default:
			r, size := utf8.DecodeRuneInString(in[i:])
			if size < 1 {
				size = 1
			}
			if r <= 0xFFFF && mustEscapeYAMLScalar(r) {
				if b.Len() == 0 {
					b.Grow(len(in) + 8)
					b.WriteString(in[:i])
				}
				writeJSONHexEscape(&b, r)
			} else if b.Len() > 0 {
				b.WriteString(in[i : i+size])
			}
			i += size
		}
	}
	if b.Len() == 0 {
		return in
	}
	return b.String()
}

// mustEscapeYAMLScalar reports whether r must be written as \uXXXX before the
// text is parsed as YAML. That is the YAML 1.1 rejected set, plus line breaks
// that a double-quoted scalar would fold.
func mustEscapeYAMLScalar(r rune) bool {
	switch r {
	case 0x0A, 0x0D, 0x85, 0x2028, 0x2029:
		return true
	case 0x09:
		return false
	}
	switch {
	case r >= 0x20 && r <= 0x7E:
		return false
	case r >= 0xA0 && r <= 0xD7FF:
		return false
	case r >= 0xE000 && r <= 0xFFFD:
		return false
	case r >= 0x10000 && r <= 0x10FFFF:
		return false
	default:
		return true
	}
}

func jsonEscapeLen(in string, i int) int {
	if i+1 >= len(in) {
		return 1
	}
	if in[i+1] != 'u' {
		return 2
	}
	if i+6 <= len(in) && isHex(in[i+2]) && isHex(in[i+3]) && isHex(in[i+4]) && isHex(in[i+5]) {
		return 6
	}
	return 2
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func writeJSONHexEscape(b *strings.Builder, r rune) {
	const hexdigits = "0123456789abcdef"
	b.WriteString(`\u`)
	b.WriteByte(hexdigits[(r>>12)&0xF])
	b.WriteByte(hexdigits[(r>>8)&0xF])
	b.WriteByte(hexdigits[(r>>4)&0xF])
	b.WriteByte(hexdigits[r&0xF])
}
