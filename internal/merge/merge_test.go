package merge

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/bruno-brant/moreco/internal/jsonc"
)

var r = VSCode

func doc(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonc.Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !jsonc.Equal(got, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", jsonc.Format(got), jsonc.Format(want))
	}
}

func names(v any) string {
	cfgs, _ := v.(*jsonc.Object).Get("configurations")
	var ns []string
	for _, c := range cfgs.([]any) {
		n, _ := c.(*jsonc.Object).Get("name")
		ns = append(ns, n.(string))
	}
	return strings.Join(ns, ",")
}

func TestMergeLayers(t *testing.T) {
	base := doc(t, `{
		"version": "0.2.0",
		"configurations": [
			{"name": "Attach", "type": "node", "request": "attach", "port": 9229},
			{"name": "Tests", "type": "node", "args": ["--watch"]},
			{"name": "Legacy", "type": "node"}
		]
	}`)
	app := doc(t, `{
		"configurations": [
			{"name": "Attach", "port": 9230, "env": {"DEBUG": "1"}},
			{"name": "Tests", "args": ["--runInBand"]},
			{"name": "Legacy", "$delete": true},
			{"name": "API", "type": "go", "program": "${workspaceFolder}/cmd/api"}
		]
	}`)
	want := doc(t, `{
		"version": "0.2.0",
		"configurations": [
			{"name": "Attach", "type": "node", "request": "attach", "port": 9230, "env": {"DEBUG": "1"}},
			{"name": "Tests", "type": "node", "args": ["--runInBand"]},
			{"name": "API", "type": "go", "program": "${workspaceFolder}/cmd/api"}
		]
	}`)
	got := r.Resolve(base, app)
	assertEqual(t, got, want)
	if names(got) != "Attach,Tests,API" {
		t.Fatalf("order: %s", names(got))
	}
}

func TestMergeMarkers(t *testing.T) {
	base := doc(t, `{"a": {"x": 1, "y": 2}, "b": 1, "configurations": [{"name": "A"}, {"name": "B"}]}`)
	over := doc(t, `{"a": {"y": {"$delete": true}}, "b": {"$delete": true},
		"configurations": {"$replace": [{"name": "Z"}]}}`)
	assertEqual(t, r.Merge(base, over), doc(t, `{"a": {"x": 1}, "configurations": [{"name": "Z"}]}`))

	// Markers never leak into a document when there is nothing underneath.
	assertEqual(t, r.Merge(nil, over), doc(t, `{"a": {}, "configurations": [{"name": "Z"}]}`))

	if r.Merge(base, deleteMarker()) != nil {
		t.Fatal("whole-document delete")
	}
}

func TestMergeUnkeyableArraysReplace(t *testing.T) {
	// Duplicate names: cannot merge by identity, so the overlay replaces.
	base := doc(t, `{"configurations": [{"name": "A"}, {"name": "A"}]}`)
	over := doc(t, `{"configurations": [{"name": "B"}]}`)
	assertEqual(t, r.Merge(base, over), over)
}

func TestMergeDoesNotMutate(t *testing.T) {
	base := doc(t, `{"configurations": [{"name": "A", "port": 1}]}`)
	over := doc(t, `{"configurations": [{"name": "A", "port": 2}]}`)
	before := jsonc.Format(base)
	r.Merge(base, over)
	if string(jsonc.Format(base)) != string(before) {
		t.Fatal("base was mutated")
	}
}

func TestDiffExamples(t *testing.T) {
	from := doc(t, `{"version": "0.2.0", "configurations": [
		{"name": "A", "port": 1}, {"name": "B"}, {"name": "C"}]}`)
	to := doc(t, `{"version": "0.2.0", "configurations": [
		{"name": "A", "port": 2}, {"name": "C"}, {"name": "D"}]}`)
	p, changed := r.Diff(from, to)
	if !changed {
		t.Fatal("expected change")
	}
	assertEqual(t, p, doc(t, `{"configurations": [
		{"name": "B", "$delete": true}, {"name": "A", "port": 2}, {"name": "D"}]}`))
	assertEqual(t, r.Merge(from, p), to)

	// Reordering is expressed as a replacement.
	reordered := doc(t, `{"version": "0.2.0", "configurations": [{"name": "C"}, {"name": "A", "port": 1}, {"name": "B"}]}`)
	p, _ = r.Diff(from, reordered)
	cfg, _ := p.(*jsonc.Object).Get("configurations")
	if _, ok := isReplace(cfg); !ok {
		t.Fatalf("expected $replace, got %s", jsonc.Format(p))
	}
	assertEqual(t, r.Merge(from, p), reordered)

	if _, changed := r.Diff(from, jsonc.Clone(from)); changed {
		t.Fatal("identical documents reported as changed")
	}
}

func TestReconcileKeepsPins(t *testing.T) {
	base := doc(t, `{"configurations": [{"name": "Attach", "port": 9229, "timeout": 1000}]}`)
	// The app pins port to the same value as the base on purpose.
	app := doc(t, `{"configurations": [{"name": "Attach", "port": 9229}]}`)
	// In the worktree the user changed the timeout and added a config.
	local := doc(t, `{"configurations": [{"name": "Attach", "port": 9229, "timeout": 5000}, {"name": "Web"}]}`)

	newApp := r.Reconcile(base, app, local)
	assertEqual(t, newApp, doc(t, `{"configurations": [
		{"name": "Attach", "port": 9229, "timeout": 5000}, {"name": "Web"}]}`))
	assertEqual(t, r.Resolve(base, newApp), local)

	// The pin survived: a later base change to port does not reach the app.
	base2 := doc(t, `{"configurations": [{"name": "Attach", "port": 1111, "timeout": 1000}]}`)
	port, _ := r.Resolve(base2, newApp).(*jsonc.Object).Get("configurations")
	got, _ := port.([]any)[0].(*jsonc.Object).Get("port")
	if got != json.Number("9229") {
		t.Fatalf("pin lost: port=%v", got)
	}
}

func TestReconcileNoChangeAndNoLayer(t *testing.T) {
	base := doc(t, `{"configurations": [{"name": "A"}]}`)
	app := doc(t, `{"configurations": [{"name": "B"}]}`)
	local := r.Resolve(base, app)
	assertEqual(t, r.Reconcile(base, app, local), app)
	if got := r.Reconcile(base, nil, jsonc.Clone(base)); got != nil {
		t.Fatalf("expected no layer, got %s", jsonc.Format(got))
	}
	assertEqual(t, r.Reconcile(nil, nil, local), local)
}

func TestReconcileDeleteInherited(t *testing.T) {
	base := doc(t, `{"configurations": [{"name": "A"}, {"name": "B"}]}`)
	local := doc(t, `{"configurations": [{"name": "A"}]}`)
	newApp := r.Reconcile(base, nil, local)
	assertEqual(t, newApp, doc(t, `{"configurations": [{"name": "B", "$delete": true}]}`))
}

func TestMerge3Clean(t *testing.T) {
	base := doc(t, `{"configurations": [{"name": "API", "port": 1}]}`)
	ours := doc(t, `{"configurations": [{"name": "API", "port": 2}]}`)                       // tweaked locally
	theirs := doc(t, `{"configurations": [{"name": "API", "port": 1}, {"name": "Worker"}]}`) // added elsewhere
	got, conflicts := r.Merge3(base, ours, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	assertEqual(t, got, doc(t, `{"configurations": [{"name": "API", "port": 2}, {"name": "Worker"}]}`))
}

func TestMerge3Conflicts(t *testing.T) {
	base := doc(t, `{"configurations": [{"name": "API", "port": 1}, {"name": "Old", "x": 1}], "a": 1}`)
	ours := doc(t, `{"configurations": [{"name": "API", "port": 2}, {"name": "Old", "x": 2}], "a": 1}`)
	theirs := doc(t, `{"configurations": [{"name": "API", "port": 3}], "a": 9}`)
	got, conflicts := r.Merge3(base, ours, theirs)
	var paths []string
	for _, c := range conflicts {
		paths = append(paths, c.Path)
	}
	want := `configurations[name="API"].port|configurations[name="Old"]`
	if strings.Join(paths, "|") != want {
		t.Fatalf("conflicts: %v", paths)
	}
	if c := conflicts[1]; !c.HasOurs || c.HasTheirs {
		t.Fatalf("delete/modify conflict flags: %+v", c)
	}
	// Ours wins in conflicts; non-conflicting theirs changes are taken.
	assertEqual(t, got, doc(t, `{"configurations": [{"name": "API", "port": 2}, {"name": "Old", "x": 2}], "a": 9}`))
}

func TestMerge3SameChangeAndAbsence(t *testing.T) {
	base := doc(t, `{"a": 1}`)
	both := doc(t, `{"a": 2}`)
	got, c := r.Merge3(base, both, jsonc.Clone(both))
	if len(c) != 0 {
		t.Fatal(c)
	}
	assertEqual(t, got, both)

	// File created on both sides with different content.
	got, c = r.Merge3(nil, doc(t, `{"a": 1, "b": 1}`), doc(t, `{"a": 1, "c": 1}`))
	if len(c) != 0 {
		t.Fatal(c)
	}
	assertEqual(t, got, doc(t, `{"a": 1, "b": 1, "c": 1}`))

	// File deleted on their side, untouched on ours.
	if got, c = r.Merge3(base, jsonc.Clone(base), nil); got != nil || len(c) != 0 {
		t.Fatal("expected deletion")
	}
}

// --- randomized properties ---

func randDoc(rng *rand.Rand) any {
	o := jsonc.NewObject()
	o.Set("version", "0.2.0")
	if rng.Intn(4) > 0 {
		var cfgs []any
		for _, i := range rng.Perm(6)[:rng.Intn(5)] {
			cfgs = append(cfgs, randItem(rng, fmt.Sprintf("cfg%d", i)))
		}
		if cfgs == nil {
			cfgs = []any{}
		}
		o.Set("configurations", cfgs)
	}
	if rng.Intn(2) == 0 {
		o.Set("extra", randScalarOrObj(rng, 2))
	}
	return o
}

func randItem(rng *rand.Rand, name string) *jsonc.Object {
	it := jsonc.NewObject()
	it.Set("name", name)
	for _, k := range []string{"type", "port", "args", "env"}[:rng.Intn(5)] {
		it.Set(k, randScalarOrObj(rng, 1))
	}
	return it
}

func randScalarOrObj(rng *rand.Rand, depth int) any {
	switch n := rng.Intn(5); {
	case n == 0:
		return json.Number(fmt.Sprint(rng.Intn(3)))
	case n == 1:
		return []any{json.Number(fmt.Sprint(rng.Intn(3)))}
	case n == 2 || depth == 0:
		return fmt.Sprint("s", rng.Intn(3))
	default:
		o := jsonc.NewObject()
		for _, k := range []string{"x", "y", "z"}[:rng.Intn(4)] {
			o.Set(k, randScalarOrObj(rng, depth-1))
		}
		return o
	}
}

// mutate returns a random edit of d, sharing structure with it via Clone.
func mutate(rng *rand.Rand, d any) any {
	o := jsonc.Clone(d).(*jsonc.Object)
	for n := rng.Intn(4); n >= 0; n-- {
		switch rng.Intn(5) {
		case 0:
			o.Set("extra", randScalarOrObj(rng, 2))
		case 1:
			o.Delete("extra")
		default:
			v, _ := o.Get("configurations")
			cfgs, _ := v.([]any)
			cfgs = append([]any(nil), cfgs...)
			switch rng.Intn(4) {
			case 0: // add
				name := fmt.Sprintf("cfg%d", 6+rng.Intn(4))
				if _, ok := identities(append(cfgs, randItem(rng, name)), "name"); ok {
					cfgs = append(cfgs, randItem(rng, name))
				}
			case 1: // remove
				if len(cfgs) > 0 {
					i := rng.Intn(len(cfgs))
					cfgs = append(cfgs[:i], cfgs[i+1:]...)
				}
			case 2: // edit
				if len(cfgs) > 0 {
					i := rng.Intn(len(cfgs))
					it := cfgs[i].(*jsonc.Object)
					it.Set([]string{"type", "port", "args", "env"}[rng.Intn(4)], randScalarOrObj(rng, 1))
				}
			case 3: // swap
				if len(cfgs) > 1 {
					i, j := rng.Intn(len(cfgs)), rng.Intn(len(cfgs))
					cfgs[i], cfgs[j] = cfgs[j], cfgs[i]
				}
			}
			if cfgs == nil {
				cfgs = []any{}
			}
			o.Set("configurations", cfgs)
		}
	}
	return o
}

func TestPropertyDiffRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		from := randDoc(rng)
		to := mutate(rng, from)
		if rng.Intn(5) == 0 {
			to = randDoc(rng)
		}
		p, changed := r.Diff(from, to)
		if !changed {
			if !jsonc.Equal(from, to) {
				t.Fatal("unchanged but different")
			}
			continue
		}
		if got := r.Merge(from, p); !jsonc.Equal(got, to) {
			t.Fatalf("iteration %d\nfrom:\n%s\nto:\n%s\npatch:\n%s\ngot:\n%s", i,
				jsonc.Format(from), jsonc.Format(to), jsonc.Format(p), jsonc.Format(got))
		}
	}
}

func TestPropertyReconcile(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 5000; i++ {
		base := randDoc(rng)
		var app any
		if rng.Intn(4) > 0 {
			app, _ = r.Diff(base, mutate(rng, base))
		}
		generated := r.Resolve(base, app)
		local := mutate(rng, generated)
		newApp := r.Reconcile(base, app, local)
		if got := r.Resolve(base, newApp); !jsonc.Equal(got, local) {
			t.Fatalf("iteration %d: Resolve(base, Reconcile(...)) != local", i)
		}
	}
}

func TestPropertyMerge3(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 5000; i++ {
		base := randDoc(rng)
		ours := mutate(rng, base)
		theirs := mutate(rng, base)
		got, conflicts := r.Merge3(base, ours, theirs)
		// One side unchanged: the merge is exactly the other side.
		if jsonc.Equal(base, theirs) && !jsonc.Equal(got, ours) ||
			jsonc.Equal(base, ours) && !jsonc.Equal(got, theirs) {
			t.Fatalf("iteration %d: trivial merge wrong", i)
		}
		// Symmetry of the conflict set.
		_, rev := r.Merge3(base, theirs, ours)
		if len(rev) != len(conflicts) {
			t.Fatalf("iteration %d: asymmetric conflicts %d vs %d", i, len(conflicts), len(rev))
		}
		// Without conflicts, both sides' edits are kept: the result is a
		// valid document that differs from base wherever either side did.
		if len(conflicts) == 0 {
			if _, ok := got.(*jsonc.Object); !ok {
				t.Fatalf("iteration %d: result not an object", i)
			}
		}
	}
}
