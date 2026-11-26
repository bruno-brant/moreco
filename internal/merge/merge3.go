package merge

import (
	"fmt"

	"github.com/bruno-brant/moreco/internal/jsonc"
)

// Conflict is a place where ours and theirs changed the same value in
// different ways. Has* report whether the value exists on that side (false
// means it was deleted or never existed).
type Conflict struct {
	Path                        string
	Base, Ours, Theirs          any
	HasBase, HasOurs, HasTheirs bool
}

func (c Conflict) String() string {
	return fmt.Sprintf("conflict at %s", c.Path)
}

// Merge3 merges two descendants of base. It walks objects key by key and keyed
// arrays item by item, so unrelated edits on each side combine cleanly. Where
// both sides changed the same value differently, the result keeps ours and a
// Conflict is reported. Nil documents mean absent; a nil result means the
// merged document is absent (deleted).
//
// Order: ours' keys and items first, then those only theirs added.
func (r Rules) Merge3(base, ours, theirs any) (any, []Conflict) {
	m := &merger3{r: r}
	res, ok := m.value("", "", base, base != nil, ours, ours != nil, theirs, theirs != nil)
	if !ok {
		res = nil
	}
	return res, m.conflicts
}

type merger3 struct {
	r         Rules
	conflicts []Conflict
}

func same(x any, xok bool, y any, yok bool) bool {
	return xok == yok && (!xok || jsonc.Equal(x, y))
}

func (m *merger3) value(path, field string, b any, bok bool, o any, ook bool, t any, tok bool) (any, bool) {
	switch {
	case same(o, ook, t, tok):
		return o, ook
	case same(b, bok, o, ook):
		return t, tok
	case same(b, bok, t, tok):
		return o, ook
	}

	if ook && tok {
		oo, ok1 := o.(*jsonc.Object)
		to, ok2 := t.(*jsonc.Object)
		if ok1 && ok2 {
			bo, ok := b.(*jsonc.Object)
			if !bok || !ok {
				bo = jsonc.NewObject()
			}
			return m.object(path, bo, oo, to), true
		}
		if kf := m.r.keyField(field); kf != "" {
			oa, ok1 := o.([]any)
			ta, ok2 := t.([]any)
			if ok1 && ok2 {
				ba, ok := b.([]any)
				if !bok || !ok {
					ba = []any{}
				}
				if res, ok := m.keyed(path, kf, ba, oa, ta); ok {
					return res, true
				}
			}
		}
	}

	m.conflicts = append(m.conflicts, Conflict{
		Path: displayPath(path),
		Base: b, Ours: o, Theirs: t,
		HasBase: bok, HasOurs: ook, HasTheirs: tok,
	})
	return o, ook
}

func (m *merger3) object(path string, b, o, t *jsonc.Object) *jsonc.Object {
	res := jsonc.NewObject()
	keys := o.Keys()
	for _, k := range t.Keys() {
		if _, ok := o.Get(k); !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		bv, bok := b.Get(k)
		ov, ook := o.Get(k)
		tv, tok := t.Get(k)
		if v, ok := m.value(joinKey(path, k), k, bv, bok, ov, ook, tv, tok); ok {
			res.Set(k, jsonc.Clone(v))
		}
	}
	return res
}

func (m *merger3) keyed(path, kf string, b, o, t []any) ([]any, bool) {
	bids, ok1 := identities(b, kf)
	oids, ok2 := identities(o, kf)
	tids, ok3 := identities(t, kf)
	if !ok1 || !ok2 || !ok3 {
		return nil, false
	}
	index := func(ids []string, items []any) map[string]any {
		mp := make(map[string]any, len(ids))
		for i, id := range ids {
			mp[id] = items[i]
		}
		return mp
	}
	bm, om, tm := index(bids, b), index(oids, o), index(tids, t)
	order := append([]string(nil), oids...)
	for _, id := range tids {
		if _, ok := om[id]; !ok {
			order = append(order, id)
		}
	}
	res := []any{}
	for _, id := range order {
		bv, bok := bm[id]
		ov, ook := om[id]
		tv, tok := tm[id]
		p := fmt.Sprintf("%s[%s=%q]", path, kf, id)
		if v, ok := m.value(p, "", bv, bok, ov, ook, tv, tok); ok {
			res = append(res, jsonc.Clone(v))
		}
	}
	return res, true
}

func joinKey(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

func displayPath(p string) string {
	if p == "" {
		return "(document)"
	}
	return p
}
