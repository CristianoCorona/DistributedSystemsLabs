package storage

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

type PathCond struct {
	lock sync.Locker
	readCond sync.Cond
	readCount int
	writeCond sync.Cond
	waitingWriters int
}

var syncMap = make(map [string]*PathCond)

func GetFile(path string, file io.Writer) error {
	p := getFilePath(path)
	entry, ok := syncMap[p]
	if !ok {
		createSyncMapEntry(p)
	}
	entry.lock.Lock()
	for entry.waitingWriters > 0 {
		entry.readCond.Wait()
	}
	entry.readCount++
	entry.lock.Unlock()

	f, err := os.Open(p)
	if err != nil {
		return err
	}
	f.WriteTo(file)

	entry.lock.Lock()
	entry.readCount--
	if entry.readCount == 0 {
		entry.writeCond.Signal()
	}
	entry.lock.Unlock()
	return nil
}

func SaveFile(path string, file io.Reader) error {
	p := getFilePath(path)

	entry, ok := syncMap[p]
	if !ok {
		createSyncMapEntry(p)
	}
	entry.lock.Lock()
	entry.waitingWriters++
	for entry.readCount > 0 {
		entry.writeCond.Wait()
	}

	f, err := os.Create(p)
	if err != nil {
		return err
	}
	if _, err := f.ReadFrom(file); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	entry.waitingWriters--
	if entry.waitingWriters > 0 {
		entry.writeCond.Signal()
	} else {
		entry.readCond.Broadcast()
	}
	entry.lock.Unlock()
	
	return nil
}



func getFilePath(path string) string {
	fileBaseDir, err := filepath.Abs("./files/")
	if err != nil {
		panic("Could not create file base dir")
	}
	return filepath.Join(fileBaseDir, path)
}

func createSyncMapEntry(path string) {
	l := &sync.Mutex{}
	syncMap[path] = &PathCond{
		lock: l,
		readCond: *sync.NewCond(l),
		readCount: 0,
		writeCond: *sync.NewCond(l),
		waitingWriters: 0,
	}
}