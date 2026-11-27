package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/rpc"
	"os"
	"path/filepath"
)

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
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.

	// uncomment to send the Example RPC to the coordinator.
	// CallExample()

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

//
// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
//
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
