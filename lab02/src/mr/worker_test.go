package mr

//
// Unit tests for the map side of the worker.
//
// Every test works in its own temporary directory (t.TempDir), which
// Go deletes automatically when the test ends.
//

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
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

// mustReadIntermediate decodes one mr-X-Y file with the production
// reader and fails the test if the file is missing or corrupted.
func mustReadIntermediate(t *testing.T, path string) []KeyValue {
	t.Helper()
	kva, err := readIntermediate(path)
	if err != nil {
		t.Fatalf("readIntermediate(%s): %v", path, err)
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
		for _, kv := range mustReadIntermediate(t, path) {
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

// countReduce is a tiny Reduce function for tests: it returns how many
// values a key has, like the word-count application does.
func countReduce(key string, values []string) string {
	return strconv.Itoa(len(values))
}

// TestDoReduceGroupsAndCounts runs two map tasks and then every reduce
// task, and checks the final output: one "key value" line per distinct
// key, each key in exactly one mr-out file, with the right count.
func TestDoReduceGroupsAndCounts(t *testing.T) {
	dir := t.TempDir()
	inputs := []string{"a b a c", "b a d"} // a:3 b:2 c:1 d:1
	want := map[string]string{"a": "3", "b": "2", "c": "1", "d": "1"}
	const nReduce = 3

	// Map phase: map task x reads input x.
	for x, text := range inputs {
		path := filepath.Join(dir, "input"+strconv.Itoa(x)+".txt")
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		reply := RequestTaskReply{Type: MapTask, TaskID: x, File: path, NReduce: nReduce}
		if err := doMap(wordMap, reply, dir); err != nil {
			t.Fatalf("doMap %d: %v", x, err)
		}
	}

	// Reduce phase, then read back every output line.
	got := map[string]string{}
	for y := 0; y < nReduce; y++ {
		reply := RequestTaskReply{Type: ReduceTask, TaskID: y, NMap: len(inputs)}
		if err := doReduce(countReduce, reply, dir); err != nil {
			t.Fatalf("doReduce %d: %v", y, err)
		}
		out := readOrEmpty(t, filepath.Join(dir, "mr-out-"+strconv.Itoa(y)))
		for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if line == "" {
				continue // an empty output file
			}
			fields := strings.Split(line, " ") // the required "%v %v" format
			if len(fields) != 2 {
				t.Fatalf("mr-out-%d: malformed line %q", y, line)
			}
			if _, dup := got[fields[0]]; dup {
				t.Fatalf("key %q appears more than once", fields[0])
			}
			got[fields[0]] = fields[1]
		}
	}

	if len(got) != len(want) {
		t.Fatalf("got %d keys %v, want %v", len(got), got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got count %q, want %q", k, got[k], v)
		}
	}
	for _, name := range listDir(t, dir) {
		if strings.HasPrefix(name, "tmp-") {
			t.Errorf("temporary file left behind: %s", name)
		}
	}
}

// TestDoReduceMissingIntermediate checks that a reduce task whose input
// is incomplete fails instead of producing a partial result.
func TestDoReduceMissingIntermediate(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte("a b"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only map task 0 runs, but the reduce task expects two map tasks.
	if err := doMap(wordMap, RequestTaskReply{Type: MapTask, TaskID: 0, File: input, NReduce: 1}, dir); err != nil {
		t.Fatal(err)
	}
	err := doReduce(countReduce, RequestTaskReply{Type: ReduceTask, TaskID: 0, NMap: 2}, dir)
	if err == nil {
		t.Fatal("doReduce with a missing mr-1-0: want an error, got nil")
	}
	if got := readOrEmpty(t, filepath.Join(dir, "mr-out-0")); got != "" {
		t.Fatalf("mr-out-0 written despite the error: %q", got)
	}
}

// TestReadIntermediateCorrupted checks that a damaged file is an error,
// not silently treated as a shorter file.
func TestReadIntermediateCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mr-0-0")
	data := `{"Key":"a","Value":"1"}` + "\n" + `{"Key":"b","Va` // truncated object
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readIntermediate(path); err == nil {
		t.Fatal("readIntermediate on a truncated file: want an error, got nil")
	}
}