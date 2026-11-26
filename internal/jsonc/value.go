// Package jsonc parses and formats JSON-with-comments documents (the format
// VS Code uses for launch.json, tasks.json, settings.json, ...).
//
// Documents are represented with plain Go values:
//
//	nil          JSON null
//	bool         JSON boolean
//	json.Number  JSON number (kept verbatim)
//	string       JSON string
//	[]any        JSON array
//	*Object      JSON object, with key order preserved
//
// Comments are not preserved: Parse drops them and Format writes plain JSON.
package jsonc

import (
	"encoding/json"
	"strconv"
)

// Object is a JSON object that remembers the order in which keys were added.
type Object struct {
	keys []string
	vals map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object {
	return &Object{vals: map[string]any{}}
}

// Len returns the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// Keys returns a copy of the keys in insertion order.
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Get returns the value stored under k.
func (o *Object) Get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// Set stores v under k. New keys are appended; existing keys keep their position.
func (o *Object) Set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// Delete removes k, if present.
func (o *Object) Delete(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Clone returns a deep copy of v.
func Clone(v any) any {
	switch t := v.(type) {
	case *Object:
		c := NewObject()
		for _, k := range t.keys {
			c.Set(k, Clone(t.vals[k]))
		}
		return c
	case []any:
		c := make([]any, len(t))
		for i, e := range t {
			c[i] = Clone(e)
		}
		return c
	default:
		return v
	}
}

// Equal reports whether a and b are the same JSON value. Object key order is
// ignored; array order is significant. Numbers compare by value.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case *Object:
		y, ok := b.(*Object)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for k, v := range x.vals {
			w, ok := y.vals[k]
			if !ok || !Equal(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		if x == y {
			return true
		}
		fx, err1 := strconv.ParseFloat(string(x), 64)
		fy, err2 := strconv.ParseFloat(string(y), 64)
		return err1 == nil && err2 == nil && fx == fy
	case string, bool, nil:
		return a == b
	}
	return false
}
