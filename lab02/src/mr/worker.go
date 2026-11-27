package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/rpc"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// waitInterval is how long a worker sleeps after a WaitTask reply before
// asking again. Short enough to pick up newly available tasks with low latency,
// long enough not to flood the coordinator with RPCs.
const waitInterval = 500 * time.Millisecond

//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}


//
// main/mrworker.go calls this function.
//
// It loops forever: ask the coordinator for a task, run it, 
// report it, and ask again, until the
// coordinator says the job is finished or can no longer be reached.
//
// A task that fails is logged and not reported: the coordinator will
// re-assign it to some worker after the timeout.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {
 
	for {
		reply, ok := requestTask()
		if !ok {
			// The coordinator is gone. In the basic part this means the job
			// is done and mrcoordinator.go has exited (see the lab text).
			return
		}
 
		switch reply.Type {
		case MapTask:
			if err := doMap(mapf, reply, "."); err != nil {
				log.Printf("worker: %v", err)
				continue // do not report: the task will time out and be re-assigned
			}
			reportTask(reply)
 
		case ReduceTask:
			if err := doReduce(reducef, reply, "."); err != nil {
				log.Printf("worker: %v", err)
				continue
			}
			reportTask(reply)
 
		case WaitTask:
			time.Sleep(waitInterval) // nothing to do yet: ask again later
 
		case ExitTask:
			return
 
		default:
			// Unknown type: a coordinator/worker mismatch. Behave like WaitTask.
			log.Printf("worker: unexpected task type %v", reply.Type)
			time.Sleep(waitInterval)
		}
	}
}

// requestTask asks the coordinator for a task.
// ok is false if the coordinator could not be reached.
func requestTask() (reply RequestTaskReply, ok bool) {
	args := RequestTaskArgs{}
	reply = RequestTaskReply{} // must be zero-valued before the call
	ok = call("Coordinator.RequestTask", &args, &reply)
	return reply, ok
}
 
// reportTask tells the coordinator that the task described by reply is
// complete. If the call fails, nothing special is done: the next
// requestTask will fail too, and the worker will stop there.
func reportTask(reply RequestTaskReply) {
	args := ReportTaskArgs{Type: reply.Type, TaskID: reply.TaskID, Attempt: reply.Attempt}
	call("Coordinator.ReportTask", &args, &ReportTaskReply{})
}

// doMap runs map task reply.TaskID on the input file reply.File and writes
// its output as reply.NReduce intermediate files mr-X-Y in directory dir,
// one per reduce task Y. Every file is created, even if empty, so that a
// reduce task can treat a missing file as an error rather than as "no keys".
//
// The output files are written atomically, so a crash leaves either the
// complete file or no file at all. doMap does not report to the
// coordinator: the caller does that, and only if doMap returns nil.
func doMap(mapf func(string, string) []KeyValue, reply RequestTaskReply, dir string) error {
	// Read the whole input split into memory, as mrsequential.go does.
	content, err := os.ReadFile(reply.File)
	if err != nil {
		return fmt.Errorf("map task %d: %w", reply.TaskID, err)
	}
 
	// Run the application's Map function: input file name and contents.
	kva := mapf(reply.File, string(content))
 
	// Split the pairs into one bucket per reduce task, by hash of the key,
	// so that every occurrence of a key ends up in the same reduce task.
	buckets := make([][]KeyValue, reply.NReduce)
	for _, kv := range kva {
		y := ihash(kv.Key) % reply.NReduce
		buckets[y] = append(buckets[y], kv)
	}
 
	// Write each bucket to its own file, mr-X-Y, as a stream of JSON objects.
	for y, bucket := range buckets {
		name := intermediateName(reply.TaskID, y)
		err := writeAtomically(dir, name, func(f *os.File) error {
			enc := json.NewEncoder(f) // one JSON object per KeyValue
			for _, kv := range bucket {
				if err := enc.Encode(&kv); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("map task %d: %w", reply.TaskID, err)
		}
	}
	return nil
}

// doReduce runs reduce task Y = reply.TaskID: it reads mr-X-Y for every
// map task X in directory dir, groups the values by key, calls the
// application's Reduce function once per key, and writes the results to
// dir/mr-out-Y, one "key value" line per key, atomically.
// Like doMap, it does not report to the coordinator.
func doReduce(reducef func(string, []string) string, reply RequestTaskReply, dir string) error {
	y := reply.TaskID
 
	// Collect every pair destined for this reduce task, from all map tasks.
	var kva []KeyValue
	for x := 0; x < reply.NMap; x++ {
		pairs, err := readIntermediate(filepath.Join(dir, intermediateName(x, y)))
		if err != nil {
			// Map tasks always create all their files, so a missing one is an error.
			return fmt.Errorf("reduce task %d: %w", y, err)
		}
		kva = append(kva, pairs...)
	}
 
	// Sort by key so that equal keys become adjacent, as in mrsequential.go.
	sort.Slice(kva, func(i, j int) bool { return kva[i].Key < kva[j].Key })
 
	// Write one output line per distinct key.
	name := fmt.Sprintf("mr-out-%d", y)
	err := writeAtomically(dir, name, func(f *os.File) error {
		i := 0
		for i < len(kva) {
			// [i, j) is the run of pairs that share the key kva[i].Key.
			j := i + 1
			for j < len(kva) && kva[j].Key == kva[i].Key {
				j++
			}
			values := make([]string, 0, j-i)
			for k := i; k < j; k++ {
				values = append(values, kva[k].Value)
			}
			output := reducef(kva[i].Key, values)
 
			// This is the line format required by the lab (see mrsequential.go).
			if _, err := fmt.Fprintf(f, "%v %v\n", kva[i].Key, output); err != nil {
				return err
			}
			i = j // move to the next key
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reduce task %d: %w", y, err)
	}
	return nil
}

// readIntermediate decodes an mr-X-Y file written by doMap back into
// key/value pairs. A missing file is an error; reaching the end of the
// stream of JSON objects is the normal way to stop.
func readIntermediate(path string) ([]KeyValue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
 
	var kva []KeyValue
	dec := json.NewDecoder(f)
	for {
		var kv KeyValue
		if err := dec.Decode(&kv); err != nil {
			if err == io.EOF {
				return kva, nil // clean end of file
			}
			return nil, fmt.Errorf("%s: %w", path, err) // corrupted file
		}
		kva = append(kva, kv)
	}
}
 
// intermediateName returns the name of the file holding the output of
// map task x destined for reduce task y.
func intermediateName(x, y int) string {
	return fmt.Sprintf("mr-%d-%d", x, y)
}
 
// writeAtomically creates the file dir/name with the contents produced by
// write, so that nobody can ever observe it half-written: the data go to a
// temporary file in the same directory, which is then renamed to name.
// Renaming within one directory is atomic, and it replaces any existing
// file with the same name (e.g. the output of an earlier attempt).
//
// The temporary name starts with "tmp-", so it never matches the patterns
// mr-X-Y or mr-out-*: a leftover from a crashed worker cannot be mistaken
// for real output (test-mr.sh reads every mr-out* file).
//
// err is a named result so that the deferred cleanup can see whether the
// function is returning an error.
func writeAtomically(dir, name string, write func(f *os.File) error) (err error) {
	f, err := os.CreateTemp(dir, "tmp-"+name+"-*") // "*" becomes a random suffix
	if err != nil {
		return err
	}
	// Runs when the function returns: on failure, remove the temporary file
	// so that it does not pile up. (Closing twice is harmless.)
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
 
	if err = write(f); err != nil {
		return err
	}
	// Close can report a failed write (e.g. disk full): check it.
	if err = f.Close(); err != nil {
		return err
	}
	// Publish the file under its final name in a single atomic step.
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

// call sends an RPC request to the coordinator and waits for the response.
// It returns true on success, false if the call failed.
//
// Changed from the lab skeleton: if the coordinator cannot be reached,
// return false instead of calling log.Fatal, so that the caller decides
// what to do (in the basic part: exit cleanly).
func call(rpcname string, args interface{}, reply interface{}) bool {
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		return false // coordinator not reachable
	}
	defer c.Close()
 
	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}
 
	log.Println(err)
	return false
}
