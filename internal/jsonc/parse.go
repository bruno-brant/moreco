package jsonc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Parse parses a JSONC document: JSON plus // and /* */ comments and trailing
// commas. A leading UTF-8 byte order mark is ignored.
func Parse(src []byte) (any, error) {
	std, err := Standardize(src)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(std))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("jsonc: empty document")
		}
		return nil, fmt.Errorf("jsonc: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("jsonc: unexpected data after top-level value")
	}
	return v, nil
}

// Standardize turns JSONC into plain JSON by blanking out comments and trailing
// commas. Byte offsets and line breaks are preserved, so decoder error
// positions still point at the original text.
func Standardize(src []byte) ([]byte, error) {
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	out := append([]byte(nil), src...)
	n := len(out)

	// Pass 1: comments.
	for i := 0; i < n; {
		switch {
		case out[i] == '"':
			i = skipString(out, i)
		case out[i] == '/' && i+1 < n && out[i+1] == '/':
			for i < n && out[i] != '\n' {
				out[i] = ' '
				i++
			}
		case out[i] == '/' && i+1 < n && out[i+1] == '*':
			j := i + 2
			for j+1 < n && !(out[j] == '*' && out[j+1] == '/') {
				j++
			}
			if j+1 >= n {
				return nil, fmt.Errorf("jsonc: unterminated block comment at offset %d", i)
			}
			for k := i; k <= j+1; k++ {
				if out[k] != '\n' && out[k] != '\r' {
					out[k] = ' '
				}
			}
			i = j + 2
		default:
			i++
		}
	}

	// Pass 2: trailing commas.
	for i := 0; i < n; {
		if out[i] == '"' {
			i = skipString(out, i)
			continue
		}
		if out[i] == ',' {
			j := i + 1
			for j < n && isSpace(out[j]) {
				j++
			}
			if j < n && (out[j] == '}' || out[j] == ']') {
				out[i] = ' '
			}
		}
		i++
	}
	return out, nil
}

// skipString returns the index just past the string literal starting at b[i].
func skipString(b []byte, i int) int {
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(b)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // string, json.Number, bool or nil
	}
	switch d {
	case '{':
		obj := NewObject()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := kt.(string)
			if !ok {
				return nil, fmt.Errorf("expected object key, got %v", kt)
			}
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			obj.Set(key, v)
		}
		if _, err := dec.Token(); err != nil { // '}'
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil { // ']'
			return nil, err
		}
		return arr, nil
	}
	return nil, fmt.Errorf("unexpected delimiter %v", d)
}
