package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"net/rpc"
	"os"
	"sort"
)

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	if os.Getenv("DEBUG") != "1" {
		log.SetOutput(io.Discard)
	}

	workerID := RegisterWorker()

	for {
		task := GetTask(workerID)
		if task.WorkerID != workerID {
			log.Printf("Worker %d received task assigned to worker %d. Ignoring.\n", workerID, task.WorkerID)
			continue
		}
		if task.Type == TaskNone {
			log.Printf("No more tasks available. Worker exiting.\n")
			break
		}
		switch task.Type {
		case TaskMap:
			log.Printf("Worker %d received Map task nr: %d\n", workerID, task.TaskID)

			mapTask(workerID, task.TaskID, task.InputFile, task.NReduce, mapf)

		case TaskReduce:
			log.Printf("Worker %d received Reduce task nr %d\n", workerID, task.TaskID)

			reduceTask(workerID, task.TaskID, task.NMap, reducef)
			TaskDone(workerID, task.TaskID, task.Type)
		default:
			log.Printf("Worker received unknown task type: %s\n", task.Type)
		}
	}

}

func RegisterWorker() (workerID int) {
	args := RegisterWorkerArgs{}
	reply := RegisterWorkerReply{}

	ok := call("Coordinator.RegisterWorker", &args, &reply)
	if !ok {
		log.Fatalf("Call to RegisterWorker failed\n")
	} else {
		//log.Printf("RegisterWorker reply: %v\n", reply)
	}

	return reply.WorkerID
}

func GetTask(workerID int) Task {
	args := GetTaskArgs{
		WorkerID: workerID,
	}
	reply := GetTaskReply{}

	ok := call("Coordinator.GetTask", &args, &reply)
	if !ok {
		log.Printf("Call to GetTask failed\n")
	} else {
		//log.Printf("GetTask reply: %v\n", reply.Task)
	}

	return reply.Task
}

func TaskDone(workerID int, taskID int, taskType TaskType) (approved bool) {
	args := TaskCompletionArgs{
		WorkerID: workerID,
		TaskID:   taskID,
		TaskType: taskType,
	}
	reply := TaskCompletionReply{}

	ok := call("Coordinator.ReportTaskCompletion", &args, &reply)
	if !ok {
		log.Printf("Call to ReportTaskCompletion failed\n")
	} else {
		//log.Printf("ReportTaskCompletion reply: %v\n", reply)
	}
	return reply.Approved
}

func TaskAck(workerID int, taskID int, taskType TaskType) {
	args := AckTaskCompletionArgs{
		WorkerID: workerID,
		TaskID:   taskID,
		TaskType: taskType,
	}
	reply := AckTaskCompletionReply{}

	ok := call("Coordinator.AckTaskCompletion", &args, &reply)
	if !ok {
		log.Printf("Call to AckTaskCompletion failed\n")
	} else {
		//log.Printf("AckTaskCompletion reply: %v\n", reply)
	}
}

// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
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

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
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

func mapTask(workerID int, taskId int, inputFile string, nReduce int, mapf func(string, string) []KeyValue) {
	// Read the input file
	content, err := os.ReadFile(inputFile)
	if err != nil {
		log.Fatalf("cannot read %v", inputFile)
	}
	// Call the Map function
	kva := mapf(inputFile, string(content))
	log.Printf("Map task %d function produced %d key-value pairs\n", taskId, len(kva))

	encoders := make([]*json.Encoder, nReduce)
	files := make([]*os.File, nReduce)
	tmpToFileName := make(map[string]string)

	for i := 0; i < nReduce; i++ {
		intermediateFileName := fmt.Sprintf("mr-%d-%d", taskId, i)
		tmpFile, err := os.CreateTemp("", intermediateFileName)
		if err != nil {
			log.Fatalf("cannot create temporary file: %v", err)
		}
		tmpToFileName[tmpFile.Name()] = intermediateFileName
		files[i] = tmpFile
		encoders[i] = json.NewEncoder(tmpFile)
	}

	defer func() {
		for _, file := range files {
			file.Close()
		}
	}()

	log.Printf("Task %d distributing %d key-value pairs to %d reduce tasks\n", taskId, len(kva), nReduce)
	size := len(kva)
	for i, kv := range kva {
		if i%(int(math.Max(1, float64(len(kva))/10))) == 0 {
			log.Printf("Progress task %d: %d/%d key-value pairs processed\n", taskId, i, size)
		}
		reduceBucket := ihash(kv.Key) % nReduce
		enc := encoders[reduceBucket]
		if err := enc.Encode(&kv); err != nil {
			log.Fatalf("cannot encode key-value pair: %v", err)
		}
	}

	approved := TaskDone(workerID, taskId, TaskMap)

	for _, file := range files {
		if !approved {
			log.Printf("Task %d completion not approved. Deleting temporary file: %s\n", taskId, file.Name())
			os.Remove(file.Name())
			continue
		}
		tmpFileName := file.Name()
		finalFileName := tmpToFileName[tmpFileName]
		if err := os.Rename(tmpFileName, finalFileName); err != nil {
			log.Fatalf("cannot rename temporary file: %v", err)
		}
	}

	TaskAck(workerID, taskId, TaskMap)

	log.Printf("Map task %d completed.\n", taskId)
}

func reduceTask(workerID int, taskId int, nMap int, reducef func(string, []string) string) {
	kva := []KeyValue{}
	for i := 0; i < nMap; i++ {
		inputFile := fmt.Sprintf("mr-%d-%d", i, taskId)
		file, err := os.Open(inputFile)
		if err != nil {
			log.Fatalf("cannot open intermediate file: %v", err)
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			kva = append(kva, kv)
		}
		file.Close()
	}

	sort.Sort(ByKey(kva))

	oname := fmt.Sprintf("mr-out-%d", taskId)
	tmpFile, err := os.CreateTemp("", oname)
	if err != nil {
		log.Fatalf("cannot create output file: %v", err)
	}
	i := 0
	for i < len(kva) {
		j := i + 1
		for j < len(kva) && kva[i].Key == kva[j].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, kva[k].Value)
		}
		result := reducef(kva[i].Key, values)
		fmt.Fprintf(tmpFile, "%v %v\n", kva[i].Key, result)
		i = j
	}
	log.Printf("Reduce task %d completed. Output written to %s, path: %s", taskId, oname, tmpFile.Name())
	tmpFile.Close()

	if !TaskDone(workerID, taskId, TaskReduce) {
		log.Printf("Reduce task %d completion not approved. Deleting temporary file: %s\n", taskId, tmpFile.Name())
		os.Remove(tmpFile.Name())
		return
	}
	if err := os.Rename(tmpFile.Name(), oname); err != nil {
		log.Fatalf("cannot rename temporary file: %v", err)
	}

	TaskAck(workerID, taskId, TaskReduce)
}
