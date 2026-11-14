package filestore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrNotFound is returned when a requested file does not exist.
	ErrNotFound = errors.New("file not found")
	// ErrBadExtension is returned when a requested file has an unsupported extension.
	ErrBadExtension = errors.New("unsupported file extension")
)

// mimeTypes maps each servable extension to its Content-Type.
var mimeTypes = map[string]string{
	".html": "text/html",
	".txt":  "text/plain",
	".gif":  "image/gif",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".css":  "text/css",
}

// Store serves and stores files under a root directory.
type Store struct {
	root string
}

// New returns a new Store that serves files under root.
func New(root string) *Store {
	return &Store{root: root}
}

// Get returns the content of the file at name and its Content-Type.
func (s *Store) Get(name string) ([]byte, string, error) {
	// extension -> content type. If not found, return ErrBadExtension
	clean := filepath.Clean("/" + name)                                // ensure the path is clean and starts with a slash
	contentType, ok := mimeTypes[strings.ToLower(filepath.Ext(clean))] // case-insensitive extension matching
	if !ok {
		return nil, "", ErrBadExtension
	}
	// Read the file from the filesystem. If it does not exist, return ErrNotFound.
	data, err := os.ReadFile(filepath.Join(s.root, clean)) // join the root directory with the cleaned path to get the full file path

	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// The requested file does not exist; return ErrNotFound
			return nil, "", ErrNotFound
		}
		// Some other error occurred while reading the file; return generic error
		return nil, "", err
	}
	return data, contentType, nil
}
