package filestore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTemp returns a Store rooted in a fresh temporary directory, which the testing
// package removes when the test ends, so no test touches the real ./data.
func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open(%q): %v", root, err)
	}
	return s, root
}

// readAll reads a body returned by Get and closes it, as every caller of Get must.
func readAll(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return string(data)
}

func TestPutThenGet(t *testing.T) {
	s, _ := openTemp(t)

	if err := s.Put("/hello.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	body, size, contentType, err := s.Get("/hello.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := readAll(t, body); got != "hello" {
		t.Errorf("body = %q, want %q", got, "hello")
	}
	// size becomes the Content-Length header, so it must match the bytes read.
	if size != 5 {
		t.Errorf("size = %d, want 5", size)
	}
	if contentType != "text/plain" {
		t.Errorf("contentType = %q, want text/plain", contentType)
	}
}

func TestPutReplacesExistingFile(t *testing.T) {
	s, _ := openTemp(t)

	// The second Put must replace the whole file, not overwrite its first bytes:
	// with an in-place write, "new" over "old version" would leave "new version".
	for _, content := range []string{"old version", "new"} {
		if err := s.Put("/f.txt", strings.NewReader(content)); err != nil {
			t.Fatalf("Put(%q): %v", content, err)
		}
	}
	body, _, _, err := s.Get("/f.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := readAll(t, body); got != "new" {
		t.Errorf("body = %q, want %q", got, "new")
	}
}

func TestContentTypes(t *testing.T) {
	s, _ := openTemp(t)

	// Extensions are matched case-insensitively; the file name itself keeps its case.
	cases := map[string]string{
		"/a.html": "text/html",
		"/a.txt":  "text/plain",
		"/a.gif":  "image/gif",
		"/a.jpeg": "image/jpeg",
		"/a.jpg":  "image/jpeg",
		"/a.css":  "text/css",
		"/B.JPG":  "image/jpeg",
	}
	for name, want := range cases {
		if err := s.Put(name, strings.NewReader("x")); err != nil {
			t.Fatalf("Put(%q): %v", name, err)
		}
		body, _, got, err := s.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		body.Close()
		if got != want {
			t.Errorf("Get(%q) contentType = %q, want %q", name, got, want)
		}
	}
}

func TestBadExtension(t *testing.T) {
	s, root := openTemp(t)

	for _, name := range []string{"/document.pdf", "/noextension", "/", "/tmp-123"} {
		if _, _, _, err := s.Get(name); !errors.Is(err, ErrBadExtension) {
			t.Errorf("Get(%q) error = %v, want ErrBadExtension", name, err)
		}
		if err := s.Put(name, strings.NewReader("x")); !errors.Is(err, ErrBadExtension) {
			t.Errorf("Put(%q) error = %v, want ErrBadExtension", name, err)
		}
	}
	// The extension is checked before the filesystem is touched: nothing was stored.
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("root contains %d entries, want 0", len(entries))
	}
}

func TestGetMissingFile(t *testing.T) {
	s, _ := openTemp(t)

	if _, _, _, err := s.Get("/missing.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
}

func TestGetDirectoryIsNotFound(t *testing.T) {
	s, root := openTemp(t)

	// A directory can be opened like a file on Linux, so Get must reject it explicitly.
	if err := os.Mkdir(filepath.Join(root, "photos.jpg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Get("/photos.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
}

func TestPathTraversalStaysInRoot(t *testing.T) {
	// The root is a subdirectory, so that a path escaping it would land in parent,
	// where the test can detect it.
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}

	// A URL path always starts with "/", but the Store must not rely on its caller for
	// that: names without the leading slash are the ones that would escape if resolve
	// cleaned the name as given instead of as an absolute path.
	for _, name := range []string{"/../escape.txt", "../escape.txt", "a/../../escape.txt"} {
		if err := s.Put(name, strings.NewReader("inside")); err != nil {
			t.Fatalf("Put(%q): %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Put(%q) wrote outside the root (Stat error = %v)", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, "escape.txt")); err != nil {
			t.Errorf("Put(%q) did not write inside the root: %v", name, err)
		}
	}

	// The same cleaning applies to reads: any number of ".." stops at the root.
	body, _, _, err := s.Get("/../../../escape.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := readAll(t, body); got != "inside" {
		t.Errorf("body = %q, want %q", got, "inside")
	}
}

func TestPutFileMode(t *testing.T) {
	s, root := openTemp(t)

	if err := s.Put("/hello.txt", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// os.CreateTemp creates files with mode 0600; Put must widen it to 0644.
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Errorf("mode = %#o, want 0644", mode)
	}
}

// failingReader returns some data and then an error, like a client that disconnects
// in the middle of an upload.
type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("connection reset")
}

func TestPutLeavesNoTempFile(t *testing.T) {
	s, root := openTemp(t)

	if err := s.Put("/ok.txt", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	// A failed upload must not leave its temporary file, nor create the target.
	if err := s.Put("/failed.txt", &failingReader{}); err == nil {
		t.Fatal("Put with a failing body succeeded, want an error")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "ok.txt" {
		t.Errorf("root contains %v, want only [ok.txt]", names)
	}
}

func TestOpenCreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a", "b", "data")

	if _, err := Open(root); err != nil {
		t.Fatalf("Open: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("root not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("root is not a directory")
	}
}

func TestOpenRemovesTempFiles(t *testing.T) {
	root := t.TempDir()

	// Leftovers of an interrupted run, and files that only look like them.
	files := []string{
		"tmp-123",       // generated by os.CreateTemp: removed
		"tmp-notes.txt", // a client file: kept
		"tmp-",          // no random number: kept
		"hello.txt",     // ordinary file: kept
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with a temp-like name is not a file Put could have left: kept.
	if err := os.Mkdir(filepath.Join(root, "tmp-456"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "tmp-123")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("tmp-123 still exists (Stat error = %v)", err)
	}
	for _, name := range []string{"tmp-notes.txt", "tmp-", "hello.txt", "tmp-456"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}
