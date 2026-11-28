package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
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
func Worker(
	mapf func(string, string) []KeyValue,
	reducef func(string, []string) string,
) {
	workerID, err := RegisterWorker()
	if err != nil {
		log.Printf("Error: registering worker: %v", err)
		return
	}

	for {
		task, err := GetTask(workerID)
		if err != nil {
			log.Printf("Error: getting task: %v", err)
			return
		}

		if task.Type == TaskNone {
			return
		}
		if task.WorkerID != workerID {
			log.Printf("Error: received task for worker %d, expected worker %d", task.WorkerID, workerID)
			continue
		}

		switch task.Type {
		case TaskMap:
			err = mapTask(workerID, task.TaskID, task.InputFile, task.NReduce, mapf)
		case TaskReduce:
			err = reduceTask(workerID, task.TaskID, task.NMap, reducef)
		default:
			err = fmt.Errorf("unknown task type %q", task.Type)
		}
		if err != nil {
			log.Printf("Error processing %s task %d: %v", task.Type, task.TaskID, err)
			return
		}
	}
}

func RegisterWorker() (int, error) {
	reply := RegisterWorkerReply{}
	if success := call("Coordinator.RegisterWorker", &RegisterWorkerArgs{}, &reply); !success {
		return 0, fmt.Errorf("RPC call to RegisterWorker failed")
	}
	return reply.WorkerID, nil
}

func GetTask(workerID int) (Task, error) {
	args := GetTaskArgs{WorkerID: workerID}
	reply := GetTaskReply{}
	if success := call("Coordinator.GetTask", &args, &reply); !success {
		return Task{}, fmt.Errorf("RPC call to GetTask failed")
	}
	return reply.Task, nil
}

func TaskDone(workerID, taskID int, taskType TaskType) (bool, error) {
	args := TaskCompletionArgs{
		WorkerID: workerID,
		TaskID:   taskID,
		TaskType: taskType,
	}
	reply := TaskCompletionReply{}
	if success := call("Coordinator.ReportTaskCompletion", &args, &reply); !success {
		return false, fmt.Errorf("RPC call to ReportTaskCompletion failed")
	}
	return reply.Approved, nil
}

func TaskAck(workerID, taskID int, taskType TaskType) error {
	args := AckTaskCompletionArgs{
		WorkerID: workerID,
		TaskID:   taskID,
		TaskType: taskType,
	}
	reply := AckTaskCompletionReply{}
	if success := call("Coordinator.AckTaskCompletion", &args, &reply); !success {
		return fmt.Errorf("RPC call to AckTaskCompletion failed")
	}
	return nil
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

func mapTask(
	workerID, taskID int,
	inputFile string,
	nReduce int,
	mapf func(string, string) []KeyValue,
) error {
	if nReduce <= 0 {
		return fmt.Errorf("invalid reduce task count %d", nReduce)
	}

	content, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("read input %q: %w", inputFile, err)
	}
	kva := mapf(inputFile, string(content))

	tempNames := make(map[string]string, nReduce)
	files := make([]*os.File, nReduce)
	encoders := make([]*json.Encoder, nReduce)
	for reduceID := range files {
		finalName := fmt.Sprintf("mr-%d-%d", taskID, reduceID)
		file, err := os.CreateTemp(".", finalName+"-*")
		if err != nil {
			return fmt.Errorf("create map output temporary file: %w", err)
		}
		files[reduceID] = file
		tempNames[file.Name()] = finalName
		encoders[reduceID] = json.NewEncoder(file)
	}

	for _, kv := range kva {
		reduceID := ihash(kv.Key) % nReduce
		if err := encoders[reduceID].Encode(&kv); err != nil {
			return fmt.Errorf("encode map output: %w", err)
		}
	}

	for i, file := range files {
		if err := file.Close(); err != nil {
			files[i] = nil
			return fmt.Errorf("close map output temporary file: %w", err)
		}
		files[i] = nil
	}

	approved, err := TaskDone(workerID, taskID, TaskMap)
	if err != nil {
		return err
	}
	if !approved {
		return fmt.Errorf("coordinator rejected map task completion")
	}

	for tempName, finalName := range tempNames {
		if err := os.Rename(tempName, finalName); err != nil {
			return fmt.Errorf("publish map output %q: %w", finalName, err)
		}
	}

	if err := TaskAck(workerID, taskID, TaskMap); err != nil {
		return err
	}
	return nil
}

func reduceTask(
	workerID, taskID, nMap int,
	reducef func(string, []string) string,
) error {
	var kva []KeyValue

	for mapID := 0; mapID < nMap; mapID++ {
		inputName := fmt.Sprintf("mr-%d-%d", mapID, taskID)
		file, err := os.Open(inputName)
		if err != nil {
			return fmt.Errorf("open intermediate file %q: %w", inputName, err)
		}

		decoder := json.NewDecoder(file)
		for {
			var kv KeyValue
			err := decoder.Decode(&kv)
			if err == io.EOF {
				break
			}
			if err != nil {
				file.Close()
				return fmt.Errorf("decode intermediate file %q: %w", inputName, err)
			}
			kva = append(kva, kv)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close intermediate file %q: %w", inputName, err)
		}
	}

	sort.Sort(ByKey(kva))

	outputName := fmt.Sprintf("mr-out-%d", taskID)
	tempFile, err := os.CreateTemp(".", outputName+"-*")
	if err != nil {
		return fmt.Errorf("create reduce output temporary file: %w", err)
	}
	tempName := tempFile.Name()

	for i := 0; i < len(kva); {
		j := i + 1
		for j < len(kva) && kva[i].Key == kva[j].Key {
			j++
		}

		values := make([]string, 0, j-i)
		for k := i; k < j; k++ {
			values = append(values, kva[k].Value)
		}

		result := reducef(kva[i].Key, values)
		if _, err := fmt.Fprintf(tempFile, "%v %v\n", kva[i].Key, result); err != nil {
			tempFile.Close()
			return fmt.Errorf("write reduce output: %w", err)
		}
		i = j
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close reduce output temporary file: %w", err)
	}

	approved, err := TaskDone(workerID, taskID, TaskReduce)
	if err != nil {
		return err
	}
	if !approved {
		return fmt.Errorf("coordinator rejected reduce task completion")
	}

	if err := os.Rename(tempName, outputName); err != nil {
		return fmt.Errorf("publish reduce output %q: %w", outputName, err)
	}

	if err := TaskAck(workerID, taskID, TaskReduce); err != nil {
		return err
	}
	return nil
}
