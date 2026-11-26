package jsonc

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Format renders v as indented JSON (tab indentation, trailing newline), with
// object keys in their stored order.
func Format(v any) []byte {
	var b bytes.Buffer
	write(&b, v, 0)
	b.WriteByte('\n')
	return b.Bytes()
}

func write(b *bytes.Buffer, v any, depth int) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		b.WriteString(string(t))
	case string:
		writeString(b, t)
	case *Object:
		if t.Len() == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range t.keys {
			b.WriteString(strings.Repeat("\t", depth+1))
			writeString(b, k)
			b.WriteString(": ")
			write(b, t.vals[k], depth+1)
			if i < len(t.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat("\t", depth))
		b.WriteByte('}')
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range t {
			b.WriteString(strings.Repeat("\t", depth+1))
			write(b, e, depth+1)
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat("\t", depth))
		b.WriteByte(']')
	default:
		// Values built in Go code (ints, floats, ...).
		enc, _ := json.Marshal(t)
		b.Write(enc)
	}
}

func writeString(b *bytes.Buffer, s string) {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Write(bytes.TrimRight(tmp.Bytes(), "\n"))
}
