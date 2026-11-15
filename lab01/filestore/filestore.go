package filestore

import (
	"errors"
	"io"
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

// resolve cleans name, checks its extension and returns the path on disk
// together with the Content-Type.
func (s *Store) resolve(name string) (path, contentType string, err error) {
	// extension -> content type. If not found, return ErrBadExtension
	clean := filepath.Clean("/" + name)                                // ensure the path is clean and starts with a slash
	contentType, ok := mimeTypes[strings.ToLower(filepath.Ext(clean))] // case-insensitive extension matching
	if !ok {
		return "", "", ErrBadExtension
	}
	return filepath.Join(s.root, clean), contentType, nil // return the full path to the file and its content type
}

// Get returns the content of the file at name and its Content-Type.
func (s *Store) Get(name string) ([]byte, string, error) {
	path, contentType, err := s.resolve(name) // resolve the file path and content type

	if err != nil {
		return nil, "", err // return the error if resolution failed
	}

	// Read the file from the filesystem. If it does not exist, return ErrNotFound.
	data, err := os.ReadFile(path)

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

// Put atomically replaces the file at name with the content of body. Concurrent readers see either the old or the new version, never a mix.
func (s *Store) Put(name string, body io.Reader) error {
	path, _, err := s.resolve(name) // resolve the file path

	if err != nil {
		return err // return the error if resolution failed
	}

	// Create a temporary file in the same directory to avoid partial writes and handle concurrent access.
	tmp, err := os.CreateTemp(filepath.Dir(path), "tmp-*") // same dir of the final file. Thanks to the extension, the file will not be served by any GET (until renamed).
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // ensure the temporary file is removed after the operation
	defer tmp.Close()           // safety net (for error paths); a second Close is harmless, but necessary in case of early returns.

	// Copy the content from the provided body to the temporary file.
	_, err = io.Copy(tmp, body)
	if err != nil {
		return err
	}

	if err := tmp.Chmod(0o644); err != nil { // set the  permissions to be readable by everyone and writable by the owner.
		return err
	}

	if err := tmp.Close(); err != nil { // ensure the temporary file is closed before renaming it
		return err
	}

	return os.Rename(tmp.Name(), path) // atomically replace the old file with the new one
}
