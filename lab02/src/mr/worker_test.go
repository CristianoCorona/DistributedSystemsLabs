package mr

//
// Unit tests for the map side of the worker.
//
// Every test works in its own temporary directory (t.TempDir), which
// Go deletes automatically when the test ends.
//

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wordMap is a tiny Map function for tests: it emits (word, "1") for every
// whitespace-separated word, like the word-count application does.
func wordMap(filename string, contents string) []KeyValue {
	var kva []KeyValue
	for _, w := range strings.Fields(contents) {
		kva = append(kva, KeyValue{Key: w, Value: "1"})
	}
	return kva
}

// readIntermediate decodes one mr-X-Y file back into key/value pairs,
// the same way a reduce task will do it.
func readIntermediate(t *testing.T, path string) []KeyValue {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("cannot open %s: %v", path, err)
	}
	defer f.Close()

	var kva []KeyValue
	dec := json.NewDecoder(f)
	for {
		var kv KeyValue
		if err := dec.Decode(&kv); err != nil {
			break // io.EOF at the end of the file
		}
		kva = append(kva, kv)
	}
	return kva
}

// listDir returns the names of the files in dir.
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestDoMapPartitionsByKey checks the core map-side properties:
// one file per reduce task (even if empty), every key in the bucket chosen
// by ihash, no pair lost, and no temporary file left behind.
func TestDoMapPartitionsByKey(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte("a b c a d e f g"), 0o644); err != nil {
		t.Fatal(err)
	}

	const nReduce = 5
	reply := RequestTaskReply{Type: MapTask, TaskID: 4, File: input, NReduce: nReduce}
	if err := doMap(wordMap, reply, dir); err != nil {
		t.Fatalf("doMap: %v", err)
	}

	total := 0
	for y := 0; y < nReduce; y++ {
		path := filepath.Join(dir, intermediateName(4, y)) // mr-4-y
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("file for reduce task %d missing: %v", y, err)
		}
		for _, kv := range readIntermediate(t, path) {
			if got := ihash(kv.Key) % nReduce; got != y {
				t.Errorf("key %q found in bucket %d, belongs to bucket %d", kv.Key, y, got)
			}
			total++
		}
	}
	if total != 8 {
		t.Errorf("read back %d pairs, want 8", total)
	}

	for _, name := range listDir(t, dir) {
		if strings.HasPrefix(name, "tmp-") {
			t.Errorf("temporary file left behind: %s", name)
		}
	}
}

// TestDoMapCreatesEmptyFiles checks that buckets with no keys still get
// their (empty) file: with a single key, all buckets but one are empty.
func TestDoMapCreatesEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	const nReduce = 4
	reply := RequestTaskReply{Type: MapTask, TaskID: 0, File: input, NReduce: nReduce}
	if err := doMap(wordMap, reply, dir); err != nil {
		t.Fatalf("doMap: %v", err)
	}
	for y := 0; y < nReduce; y++ {
		if _, err := os.Stat(filepath.Join(dir, intermediateName(0, y))); err != nil {
			t.Errorf("file for reduce task %d missing: %v", y, err)
		}
	}
}

// TestDoMapMissingInput checks that an unreadable input is reported as an
// error and produces no intermediate file.
func TestDoMapMissingInput(t *testing.T) {
	dir := t.TempDir()
	reply := RequestTaskReply{Type: MapTask, TaskID: 0, File: filepath.Join(dir, "nope.txt"), NReduce: 3}
	if err := doMap(wordMap, reply, dir); err == nil {
		t.Fatal("doMap with a missing input file: want an error, got nil")
	}
	if names := listDir(t, dir); len(names) != 0 {
		t.Fatalf("files created despite the error: %v", names)
	}
}

// TestWriteAtomicallyFailureLeavesNothing checks that when the writer
// fails, neither the final file nor the temporary file remains.
func TestWriteAtomicallyFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	boom := errors.New("boom")
	err := writeAtomically(dir, "mr-0-0", func(f *os.File) error {
		f.WriteString("partial data") // something was written before failing
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want the writer's error, got %v", err)
	}
	if names := listDir(t, dir); len(names) != 0 {
		t.Fatalf("files left after a failed write: %v", names)
	}
}

// readOrEmpty returns the contents of path, or "" if it does not exist.
func readOrEmpty(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestWriteAtomicallyIsInvisibleUntilDone checks the property that makes
// the function "atomic": while the data are being written, a reader of the
// final name sees either nothing or the previous complete version, never
// the partial new one. It also checks that a re-executed task replaces
// the output of an earlier attempt.
func TestWriteAtomicallyIsInvisibleUntilDone(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "mr-0-0")

	// First attempt: while writing, the final file must not exist yet.
	err := writeAtomically(dir, "mr-0-0", func(f *os.File) error {
		f.WriteString("first attempt")
		if got := readOrEmpty(t, final); got != "" {
			t.Errorf("during the first write, reader saw %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second attempt: while writing, the reader must still see the first version.
	err = writeAtomically(dir, "mr-0-0", func(f *os.File) error {
		f.WriteString("second")
		if got := readOrEmpty(t, final); got != "first attempt" {
			t.Errorf("during the second write, reader saw %q", got)
		}
		f.WriteString(" attempt")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := readOrEmpty(t, final); got != "second attempt" {
		t.Fatalf("after the second write: %q, want %q", got, "second attempt")
	}
	if names := listDir(t, dir); len(names) != 1 {
		t.Fatalf("want only mr-0-0 in the directory, got %v", names)
	}
}

// TestWriteAtomicallyFailureKeepsOldFile checks that a failed attempt does
// not damage the output of an earlier successful attempt.
func TestWriteAtomicallyFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "mr-0-0")
	if err := os.WriteFile(final, []byte("good output"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeAtomically(dir, "mr-0-0", func(f *os.File) error {
		f.WriteString("garbage")
		return errors.New("crash in the middle")
	})

	if got := readOrEmpty(t, final); got != "good output" {
		t.Fatalf("old output changed to %q", got)
	}
}