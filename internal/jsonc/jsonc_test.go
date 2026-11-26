package jsonc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseJSONC(t *testing.T) {
	src := "\xef\xbb\xbf" + `{
	// Use IntelliSense to learn about possible attributes.
	"version": "0.2.0", /* block
	comment */
	"url": "http://localhost:3000/a//b", // the // inside the string stays
	"tricky": "quote \" and /* not a comment */",
	"configurations": [
		{ "name": "A", "port": 9229, },
	],
}`
	v, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(*Object)
	if got := strings.Join(obj.Keys(), ","); got != "version,url,tricky,configurations" {
		t.Fatalf("key order: %s", got)
	}
	if u, _ := obj.Get("url"); u != "http://localhost:3000/a//b" {
		t.Fatalf("url: %v", u)
	}
	if s, _ := obj.Get("tricky"); s != `quote " and /* not a comment */` {
		t.Fatalf("tricky: %v", s)
	}
	cfgs, _ := obj.Get("configurations")
	port, _ := cfgs.([]any)[0].(*Object).Get("port")
	if port != json.Number("9229") {
		t.Fatalf("port: %#v", port)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"", "  // only a comment", "{} {}", "{ /* open", `{"a": }`} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}

func TestFormatRoundTrip(t *testing.T) {
	src := `{"b": [1, 2.50, {"x": null}], "a": {"<html>": true, "e": {}, "f": []}}`
	v, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := Format(v)
	want := "{\n\t\"b\": [\n\t\t1,\n\t\t2.50,\n\t\t{\n\t\t\t\"x\": null\n\t\t}\n\t],\n\t\"a\": {\n\t\t\"<html>\": true,\n\t\t\"e\": {},\n\t\t\"f\": []\n\t}\n}\n"
	if string(out) != want {
		t.Fatalf("format:\n%s\nwant:\n%s", out, want)
	}
	back, err := Parse(out)
	if err != nil || !Equal(v, back) {
		t.Fatalf("round trip failed: %v", err)
	}
}

func TestEqual(t *testing.T) {
	a, _ := Parse([]byte(`{"x": 1, "y": [1, 2]}`))
	b, _ := Parse([]byte(`{"y": [1, 2.0], "x": 1}`))
	c, _ := Parse([]byte(`{"y": [2, 1], "x": 1}`))
	if !Equal(a, b) {
		t.Error("key order / number format should not matter")
	}
	if Equal(a, c) {
		t.Error("array order should matter")
	}
	if Equal("1", json.Number("1")) {
		t.Error("string vs number")
	}
}
