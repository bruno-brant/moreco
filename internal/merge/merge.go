// Package merge implements moreco's semantic operations on JSON documents:
//
//   - Merge / Resolve: apply layers (overlays) on top of each other, so an app's
//     launch.json can inherit from a shared base.
//   - Diff: compute the overlay that turns one document into another.
//   - Reconcile: rewrite an app layer so that base + layer equals what the
//     user has on disk, preserving the existing layer where possible.
//   - Merge3: three-way merge (base / ours / theirs) used when pulling into a
//     worktree that has local edits.
//
// # Overlay semantics
//
// An overlay is a JSON document applied to a base document:
//
//   - objects merge key by key, recursively;
//   - arrays whose field is listed in Rules.ArrayKeys (e.g. "configurations"
//     keyed by "name") merge item by item, matched on that identity field;
//     new items are appended;
//   - every other value (scalars, other arrays) replaces the base value;
//   - {"$delete": true} as a value removes that key; an array item
//     {"name": "X", "$delete": true} removes the item named X;
//   - {"$replace": V} replaces the base value with V outright, without merging.
//
// In this package a nil document means "absent" (no such file in that layer).
package merge

import (
	"github.com/bruno-brant/moreco/internal/jsonc"
)

// Marker keys understood in overlays.
const (
	DeleteKey  = "$delete"
	ReplaceKey = "$replace"
)

// Rules says which arrays are merged by identity rather than replaced.
type Rules struct {
	// ArrayKeys maps an object field name to the identity field of the
	// objects in the array stored under it.
	ArrayKeys map[string]string
}

// VSCode is the rule set for VS Code's launch.json and tasks.json.
var VSCode = Rules{ArrayKeys: map[string]string{
	"configurations": "name",  // launch.json
	"compounds":      "name",  // launch.json
	"tasks":          "label", // tasks.json
	"inputs":         "id",    // launch.json and tasks.json
}}

func (r Rules) keyField(field string) string {
	if field == "" {
		return ""
	}
	return r.ArrayKeys[field]
}

// Merge applies overlay on top of base and returns the result. Neither input
// is modified. A nil base means the base is absent; a nil result means the
// overlay deleted the whole document.
func (r Rules) Merge(base, overlay any) any {
	if overlay == nil {
		return jsonc.Clone(base)
	}
	v, ok := r.apply("", base, base != nil, overlay)
	if !ok {
		return nil
	}
	return v
}

// Resolve folds layers from lowest to highest. Nil layers are skipped.
func (r Rules) Resolve(layers ...any) any {
	var cur any
	for _, l := range layers {
		if l != nil {
			cur = r.Merge(cur, l)
		}
	}
	return cur
}

// apply merges patch p onto x (present when xok). field is the name of the
// object field holding x, used to pick array identity rules.
func (r Rules) apply(field string, x any, xok bool, p any) (any, bool) {
	if isDelete(p) {
		return nil, false
	}
	if v, ok := isReplace(p); ok {
		return strip(v), true
	}
	switch pt := p.(type) {
	case *jsonc.Object:
		if xo, ok := x.(*jsonc.Object); xok && ok {
			return r.mergeObject(xo, pt), true
		}
		return strip(pt), true
	case []any:
		if kf := r.keyField(field); kf != "" && xok {
			if xa, ok := x.([]any); ok {
				if res, ok := r.mergeKeyed(kf, xa, pt); ok {
					return res, true
				}
			}
		}
		return strip(pt), true
	}
	return p, true
}

func (r Rules) mergeObject(x, p *jsonc.Object) *jsonc.Object {
	res := jsonc.Clone(x).(*jsonc.Object)
	for _, k := range p.Keys() {
		pv, _ := p.Get(k)
		xv, xok := res.Get(k)
		if v, ok := r.apply(k, xv, xok, pv); ok {
			res.Set(k, v)
		} else {
			res.Delete(k)
		}
	}
	return res
}

// mergeKeyed merges overlay items into base items by identity. It reports
// false if either array is not keyable, in which case the caller replaces.
func (r Rules) mergeKeyed(kf string, x, p []any) ([]any, bool) {
	xids, ok := identities(x, kf)
	if !ok {
		return nil, false
	}
	pids, ok := identities(p, kf)
	if !ok {
		return nil, false
	}
	res := jsonc.Clone(x).([]any)
	pos := make(map[string]int, len(xids))
	for i, id := range xids {
		pos[id] = i
	}
	removed := map[string]bool{}
	for i, item := range p {
		id := pids[i]
		j, exists := pos[id]
		switch {
		case isDelete(item):
			if exists {
				removed[id] = true
			}
		case exists && removed[id]:
			// Deleted and re-added in the same overlay: replace in place.
			res[j] = strip(item)
			delete(removed, id)
		case exists:
			res[j], _ = r.apply("", res[j], true, item)
		default:
			pos[id] = len(res)
			res = append(res, strip(item))
		}
	}
	if len(removed) == 0 {
		return res, true
	}
	out := res[:0]
	for _, item := range res {
		id, _ := item.(*jsonc.Object).Get(kf)
		if !removed[id.(string)] {
			out = append(out, item)
		}
	}
	return out, true
}

// strip returns a copy of v with overlay markers resolved against nothing:
// deletions dropped, replacements unwrapped.
func strip(v any) any {
	if rv, ok := isReplace(v); ok {
		return strip(rv)
	}
	switch t := v.(type) {
	case *jsonc.Object:
		res := jsonc.NewObject()
		for _, k := range t.Keys() {
			val, _ := t.Get(k)
			if !isDelete(val) {
				res.Set(k, strip(val))
			}
		}
		return res
	case []any:
		res := make([]any, 0, len(t))
		for _, e := range t {
			if !isDelete(e) {
				res = append(res, strip(e))
			}
		}
		return res
	}
	return v
}

func isDelete(v any) bool {
	o, ok := v.(*jsonc.Object)
	if !ok {
		return false
	}
	d, ok := o.Get(DeleteKey)
	return ok && d == true
}

func isReplace(v any) (any, bool) {
	o, ok := v.(*jsonc.Object)
	if !ok || o.Len() != 1 {
		return nil, false
	}
	return o.Get(ReplaceKey)
}

func deleteMarker() *jsonc.Object {
	o := jsonc.NewObject()
	o.Set(DeleteKey, true)
	return o
}

func itemDeleteMarker(kf, id string) *jsonc.Object {
	o := jsonc.NewObject()
	o.Set(kf, id)
	o.Set(DeleteKey, true)
	return o
}

func replaceMarker(v any) *jsonc.Object {
	o := jsonc.NewObject()
	o.Set(ReplaceKey, v)
	return o
}

// identities returns the identity of every item, or false if some item is not
// an object with a unique string identity field.
func identities(arr []any, kf string) ([]string, bool) {
	ids := make([]string, len(arr))
	seen := make(map[string]bool, len(arr))
	for i, item := range arr {
		o, ok := item.(*jsonc.Object)
		if !ok {
			return nil, false
		}
		v, ok := o.Get(kf)
		if !ok {
			return nil, false
		}
		s, ok := v.(string)
		if !ok || seen[s] {
			return nil, false
		}
		seen[s] = true
		ids[i] = s
	}
	return ids, true
}
