package main

import (
	"errors"
	"strings"
)

// shellWords splits s into words as a POSIX shell would, honouring quotes
// and backslashes, without expanding anything.
func shellWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		var err error
		switch c := s[i]; c {
		case '\'':
			i, err = singleQuoted(s, i, &cur)
		case '"':
			i, err = doubleQuoted(s, i, &cur)
		case '\\':
			if i+1 < len(s) {
				i++
			}
			cur.WriteByte(s[i])
		case ' ', '\t', '\n', '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
			}
			inWord = false
			continue
		default:
			cur.WriteByte(c)
		}
		if err != nil {
			return nil, err
		}
		inWord = true
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// singleQuoted copies the quoted text starting at s[i] and returns the index
// of the closing quote.
func singleQuoted(s string, i int, cur *strings.Builder) (int, error) {
	end := strings.IndexByte(s[i+1:], '\'')
	if end < 0 {
		return i, errors.New("unterminated single quote")
	}
	cur.WriteString(s[i+1 : i+1+end])
	return i + 1 + end, nil
}

// doubleQuoted is singleQuoted for double quotes, where a backslash escapes
// the characters a shell lets it escape there.
func doubleQuoted(s string, i int, cur *strings.Builder) (int, error) {
	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == '"':
			return j, nil
		case s[j] == '\\' && j+1 < len(s) && strings.IndexByte("\"\\$`", s[j+1]) >= 0:
			j++
		}
		cur.WriteByte(s[j])
	}
	return i, errors.New("unterminated double quote")
}

// joinArgs quotes args for a log line.
func joinArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'\\$;&|<>*?") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}
