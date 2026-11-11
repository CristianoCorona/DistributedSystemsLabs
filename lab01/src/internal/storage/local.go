package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// TODO: add function to save image

func GetImage(path string, file *os.File) error {
	imageBaseDir, err := filepath.Abs("./images/")
	if err != nil {
		panic("Could not create image base dir")
	}
	fmt.Println(filepath.Join(imageBaseDir, path))
	val, err := os.Open(filepath.Join(imageBaseDir, path))
	if err != nil {
		return os.ErrNotExist
	}
	*file = *val
	return nil
}