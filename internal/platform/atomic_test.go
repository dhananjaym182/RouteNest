package platform

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFileAtomicDurableReplacesExistingAndCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomicDurable(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("persisted = %q, err = %v", data, err)
	}
	assertNoAtomicTemps(t, dir, path)
}

func TestWriteFileAtomicDurablePreReplacementFailurePreservesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	err := WriteFileAtomicDurableWithOptions(path, []byte("new"), 0600, AtomicWriteOptions{Fault: func(stage AtomicWriteStage) error {
		if stage == AtomicWriteRename {
			return errors.New("injected")
		}
		return nil
	}})
	if err == nil || AtomicWriteMayHaveReplaced(err) {
		t.Fatalf("error = %v, want pre-replacement failure", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "old" {
		t.Fatalf("persisted = %q, err = %v", data, readErr)
	}
	assertNoAtomicTemps(t, dir, path)
}

func TestWriteFileAtomicDurablePreReplacementStagesPreserveTarget(t *testing.T) {
	stages := []AtomicWriteStage{
		AtomicWritePermission,
		AtomicWriteData,
		AtomicWriteFileSync,
		AtomicWriteRename,
	}
	for _, failedStage := range stages {
		t.Run(string(failedStage), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.json")
			if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			err := WriteFileAtomicDurableWithOptions(path, []byte("new"), 0600, AtomicWriteOptions{Fault: func(stage AtomicWriteStage) error {
				if stage == failedStage {
					return errors.New("injected")
				}
				return nil
			}})
			if err == nil || AtomicWriteMayHaveReplaced(err) {
				t.Fatalf("error = %v, want pre-replacement failure", err)
			}
			var writeErr *AtomicWriteError
			if !errors.As(err, &writeErr) || writeErr.Stage != failedStage {
				t.Fatalf("error = %v, want stage %s", err, failedStage)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != "old" {
				t.Fatalf("persisted = %q, err = %v", data, readErr)
			}
			assertNoAtomicTemps(t, dir, path)
		})
	}
}

func TestWriteFileAtomicDurablePostReplacementFailureReportsUncertainty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	err := WriteFileAtomicDurableWithOptions(path, []byte("new"), 0600, AtomicWriteOptions{Fault: func(stage AtomicWriteStage) error {
		if stage == AtomicWriteDirectorySync {
			return errors.New("injected")
		}
		return nil
	}})
	if err == nil || !AtomicWriteMayHaveReplaced(err) {
		t.Fatalf("error = %v, want post-replacement uncertainty", err)
	}
	var writeErr *AtomicWriteError
	if !errors.As(err, &writeErr) || writeErr.Stage != AtomicWriteDirectorySync {
		t.Fatalf("error = %v, want directory-sync stage", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "new" {
		t.Fatalf("persisted = %q, err = %v", data, readErr)
	}
	assertNoAtomicTemps(t, dir, path)
}

func TestWriteFileAtomicDurableConcurrentWritesStayWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 8192),
		bytes.Repeat([]byte("b"), 12288),
		bytes.Repeat([]byte("c"), 16384),
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(payloads))
	for _, payload := range payloads {
		payload := payload
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- WriteFileAtomicDurable(path, payload, 0600)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	whole := false
	for _, payload := range payloads {
		whole = whole || bytes.Equal(data, payload)
	}
	if !whole {
		t.Fatalf("concurrent result was torn: %d bytes", len(data))
	}
	assertNoAtomicTemps(t, dir, path)
}

func assertNoAtomicTemps(t *testing.T, dir, path string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "."+filepath.Base(path)+"-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}
