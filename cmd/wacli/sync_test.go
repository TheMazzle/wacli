package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMediaFileExists(t *testing.T) {
	t.Run("empty path returns false", func(t *testing.T) {
		if mediaFileExists("") {
			t.Error("expected false for empty path")
		}
	})

	t.Run("missing file returns false", func(t *testing.T) {
		if mediaFileExists("/nonexistent/path/image.jpe") {
			t.Error("expected false for non-existent file")
		}
	})

	t.Run("existing file returns true", func(t *testing.T) {
		tmp := filepath.Join(t.TempDir(), "image.jpe")
		if err := os.WriteFile(tmp, []byte("fake jpeg"), 0600); err != nil {
			t.Fatal(err)
		}
		if !mediaFileExists(tmp) {
			t.Errorf("expected true for existing file %s", tmp)
		}
	})

	t.Run("path set but file deleted returns false", func(t *testing.T) {
		tmp := filepath.Join(t.TempDir(), "deleted.jpe")
		if err := os.WriteFile(tmp, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		os.Remove(tmp)
		if mediaFileExists(tmp) {
			t.Error("expected false after file is deleted")
		}
	})
}
