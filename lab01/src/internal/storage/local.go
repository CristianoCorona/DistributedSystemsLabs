package storage

import (
	"io"
	"os"
	"path/filepath"
)

func GetImage(path string, file io.Writer) error {
	p := getImagePath(path)
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	f.WriteTo(file)
	return nil
}

func SaveImage(path string, file io.Reader) error {
	f, err := os.Create(getImagePath(path))
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

func getImagePath(path string) string {
	imageBaseDir, err := filepath.Abs("./images/")
	if err != nil {
		panic("Could not create image base dir")
	}
	return filepath.Join(imageBaseDir, path)
}