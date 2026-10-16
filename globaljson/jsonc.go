package globaljson

import "bytes"

// StripJSONC turns JSON with comments and trailing commas into plain JSON,
// leaving string contents alone. A leading byte order mark is dropped too.
func StripJSONC(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case c == '"':
			end := stringEnd(data, i)
			out = append(out, data[i:end]...)
			i = end - 1
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			end := bytes.Index(data[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			i += end + 3
			out = append(out, ' ')
		default:
			out = append(out, c)
		}
	}
	return dropTrailingCommas(out)
}

// stringEnd returns the index just past the string literal starting at i.
func stringEnd(data []byte, i int) int {
	for j := i + 1; j < len(data); j++ {
		switch data[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return len(data)
}

func dropTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c == '"' {
			end := stringEnd(data, i)
			out = append(out, data[i:end]...)
			i = end - 1
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(data) && isSpace(data[j]) {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
