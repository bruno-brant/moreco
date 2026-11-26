package merge

import "github.com/bruno-brant/moreco/internal/jsonc"

// Diff returns an overlay p such that Merge(from, p) equals to, and whether
// the documents differ at all. Both documents must be present (non-nil).
//
// Keyed arrays are diffed item by item when the result can be expressed that
// way: surviving items keep their relative order and new items come last.
// Any other reordering is encoded as a $replace of the whole array.
func (r Rules) Diff(from, to any) (any, bool) {
	return r.diff("", from, to)
}

func (r Rules) diff(field string, from, to any) (any, bool) {
	if jsonc.Equal(from, to) {
		return nil, false
	}
	if fo, ok := from.(*jsonc.Object); ok {
		if tobj, ok := to.(*jsonc.Object); ok {
			return r.diffObject(fo, tobj), true
		}
	}
	if kf := r.keyField(field); kf != "" {
		fa, ok1 := from.([]any)
		ta, ok2 := to.([]any)
		if ok1 && ok2 {
			if p, ok := r.diffKeyed(kf, fa, ta); ok {
				return p, true
			}
		}
		if _, ok := to.([]any); ok {
			// A plain array would be merged by identity; force replacement.
			return replaceMarker(jsonc.Clone(to)), true
		}
	}
	return jsonc.Clone(to), true
}

func (r Rules) diffObject(from, to *jsonc.Object) *jsonc.Object {
	patch := jsonc.NewObject()
	for _, k := range to.Keys() {
		tv, _ := to.Get(k)
		if fv, ok := from.Get(k); ok {
			if p, changed := r.diff(k, fv, tv); changed {
				patch.Set(k, p)
			}
		} else {
			patch.Set(k, jsonc.Clone(tv))
		}
	}
	for _, k := range from.Keys() {
		if _, ok := to.Get(k); !ok {
			patch.Set(k, deleteMarker())
		}
	}
	return patch
}

func (r Rules) diffKeyed(kf string, from, to []any) (any, bool) {
	fids, ok := identities(from, kf)
	if !ok {
		return nil, false
	}
	tids, ok := identities(to, kf)
	if !ok {
		return nil, false
	}
	fitems := make(map[string]any, len(from))
	for i, id := range fids {
		fitems[id] = from[i]
	}
	tset := make(map[string]bool, len(tids))
	for _, id := range tids {
		tset[id] = true
	}

	// The item-wise form can only express "survivors in original order,
	// then additions". Check that "to" has that shape.
	var survivors []string
	for _, id := range fids {
		if tset[id] {
			survivors = append(survivors, id)
		}
	}
	for i, id := range tids {
		_, existed := fitems[id]
		if i < len(survivors) {
			if id != survivors[i] {
				return nil, false
			}
		} else if existed {
			return nil, false
		}
	}

	patch := []any{}
	for _, id := range fids {
		if !tset[id] {
			patch = append(patch, itemDeleteMarker(kf, id))
		}
	}
	for i, id := range tids {
		fitem, existed := fitems[id]
		if !existed {
			patch = append(patch, jsonc.Clone(to[i]))
			continue
		}
		p, changed := r.diff("", fitem, to[i])
		if !changed {
			continue
		}
		// Put the identity first so the overlay item can be matched.
		item := jsonc.NewObject()
		item.Set(kf, id)
		po := p.(*jsonc.Object)
		for _, k := range po.Keys() {
			v, _ := po.Get(k)
			item.Set(k, v)
		}
		patch = append(patch, item)
	}
	return patch, true
}
