package merge

import "github.com/bruno-brant/moreco/internal/jsonc"

// Reconcile returns a new upper layer U' such that Resolve(lower, U') equals
// target. This is how a push folds the edits made in a worktree back into the
// app's layer.
//
// lower is the resolved document below the layer (nil if absent), upper is the
// layer's current content (nil if the layer has no file yet) and target is
// what the worktree has on disk (must be non-nil).
//
// The existing layer is preserved where it can be: only the parts the user
// actually changed are rewritten, so values the layer pins on purpose (even
// when equal to the base) stay pinned. If the edit cannot be expressed on top
// of the old layer, the layer is recomputed from scratch as Diff(lower, target).
// A nil result means no layer is needed (target equals lower).
func (r Rules) Reconcile(lower, upper, target any) any {
	cur := r.Resolve(lower, upper)
	if cur != nil && jsonc.Equal(cur, target) {
		return jsonc.Clone(upper)
	}
	if lower == nil {
		return jsonc.Clone(target)
	}

	if upper != nil && cur != nil {
		patch, _ := r.Diff(cur, target)
		if np, ok := r.compose("", upper, patch); ok {
			if res := r.Resolve(lower, np); res != nil && jsonc.Equal(res, target) {
				return np
			}
		}
	}

	p, changed := r.Diff(lower, target)
	if !changed {
		return nil
	}
	return p
}

// compose returns a single overlay equivalent to applying p1 then p2. It
// reports false for combinations it cannot express; callers verify the result
// and fall back to recomputing the layer.
func (r Rules) compose(field string, p1, p2 any) (any, bool) {
	if isDelete(p2) {
		return jsonc.Clone(p2), true
	}
	if _, ok := isReplace(p2); ok {
		return jsonc.Clone(p2), true
	}
	if isDelete(p1) {
		return replaceMarker(strip(p2)), true
	}
	if v1, ok := isReplace(p1); ok {
		res, _ := r.apply(field, v1, true, p2)
		return replaceMarker(res), true
	}

	switch t2 := p2.(type) {
	case *jsonc.Object:
		o1, ok := p1.(*jsonc.Object)
		if !ok {
			return replaceMarker(strip(t2)), true
		}
		res := jsonc.Clone(o1).(*jsonc.Object)
		for _, k := range t2.Keys() {
			v2, _ := t2.Get(k)
			if v1, ok := res.Get(k); ok {
				v, ok := r.compose(k, v1, v2)
				if !ok {
					return nil, false
				}
				res.Set(k, v)
			} else {
				res.Set(k, jsonc.Clone(v2))
			}
		}
		return res, true

	case []any:
		kf := r.keyField(field)
		if kf == "" {
			return jsonc.Clone(t2), true
		}
		ids2, ok := identities(t2, kf)
		if !ok {
			return jsonc.Clone(t2), true // replaces whatever p1 produced
		}
		a1, ok := p1.([]any)
		if !ok {
			return replaceMarker(strip(t2)), true
		}
		ids1, ok := identities(a1, kf)
		if !ok {
			return replaceMarker(strip(t2)), true
		}
		res := jsonc.Clone(a1).([]any)
		pos := make(map[string]int, len(ids1))
		for i, id := range ids1 {
			pos[id] = i
		}
		for i, item2 := range t2 {
			id := ids2[i]
			j, ok := pos[id]
			if !ok {
				pos[id] = len(res)
				res = append(res, jsonc.Clone(item2))
				continue
			}
			item1 := res[j]
			switch {
			case isDelete(item2):
				res[j] = jsonc.Clone(item2)
			case isDelete(item1):
				return nil, false // delete then re-add: not expressible item-wise
			default:
				v, ok := r.compose("", item1, item2)
				if !ok {
					return nil, false
				}
				res[j] = v
			}
		}
		return res, true
	}
	return jsonc.Clone(p2), true
}
