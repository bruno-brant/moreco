package pathtok

import "testing"

func TestUnix(t *testing.T) {
	main := New("/src/mono")
	in := `{
	"program": "/src/mono/apps/api/main.go",
	"cwd": "/src/mono",
	"other": "/src/mono2/x",
	"rel": "x/src/mono/y",
	"uri": "file:///src/mono/a",
	"var": "${workspaceFolder}/a"
}`
	enc := string(main.Encode([]byte(in)))
	want := `{
	"program": "{{moreco:root}}/apps/api/main.go",
	"cwd": "{{moreco:root}}",
	"other": "/src/mono2/x",
	"rel": "x/src/mono/y",
	"uri": "file://{{moreco:root}}/a",
	"var": "${workspaceFolder}/a"
}`
	if enc != want {
		t.Fatalf("encode:\n%s", enc)
	}
	if got := string(main.Decode([]byte(enc))); got != in {
		t.Fatalf("decode:\n%s", got)
	}

	// In a worktree, references to the main checkout are encoded too and
	// decode to the worktree.
	wt := New("/src/mono-feature", "/src/mono")
	out := string(wt.Decode(wt.Encode([]byte(`"/src/mono/a" "/src/mono-feature/b"`))))
	if out != `"/src/mono-feature/a" "/src/mono-feature/b"` {
		t.Fatalf("worktree: %s", out)
	}
}

func TestWindows(t *testing.T) {
	c := New(`C:\src\mono`)
	in := `{"a": "C:\\src\\mono\\x", "b": "c:/src/mono/y", "c": "C:\src\mono"}`
	enc := string(c.Encode([]byte(in)))
	want := `{"a": "{{moreco:root:json}}\\x", "b": "{{moreco:root}}/y", "c": "{{moreco:root:win}}"}`
	if enc != want {
		t.Fatalf("encode: %s", enc)
	}
	other := New(`D:\work\mono`)
	if got := string(other.Decode([]byte(enc))); got != `{"a": "D:\\work\\mono\\x", "b": "D:/work/mono/y", "c": "D:\work\mono"}` {
		t.Fatalf("decode: %s", got)
	}
	// Decoded on Linux, every form becomes a forward-slash path.
	if got := string(New("/home/me/mono").Decode([]byte(enc))); got != `{"a": "/home/me/mono\\x", "b": "/home/me/mono/y", "c": "/home/me/mono"}` {
		t.Fatalf("cross-platform decode: %s", got)
	}
}

func TestNoMatchUnchanged(t *testing.T) {
	c := New("/src/mono/")
	in := []byte("nothing here")
	if got := c.Encode(in); string(got) != "nothing here" {
		t.Fatal(string(got))
	}
}
