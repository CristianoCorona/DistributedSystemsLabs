package storage

import (
	"io"
	"os"
	"path/filepath"
)

func GetFile(path string, file io.Writer) error {
	p := getFilePath(path)
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	f.WriteTo(file)
	return nil
}

func SaveFile(path string, file io.Reader) error {
	f, err := os.Create(getFilePath(path))
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
	
	return nil
}

func getFilePath(path string) string {
	fileBaseDir, err := filepath.Abs("./files/")
	if err != nil {
		panic("Could not create file base dir")
	}
	return filepath.Join(fileBaseDir, path)
}