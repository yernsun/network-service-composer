package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func canonicalPlacement(path string) (string, error) {
	remaining := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(append([]string{resolved}, remaining...)...), nil
		}
		if !os.IsNotExist(err) || path == filepath.Dir(path) {
			return "", err
		}
		remaining = append([]string{filepath.Base(path)}, remaining...)
		path = filepath.Dir(path)
	}
}

// ValidateMetadataReferences keeps private runtime/configuration material out of
// the one directory mounted by the independent executor, including path aliases.
func ValidateMetadataReferences(directory string, references ...string) error {
	root, err := canonicalPlacement(directory)
	if err != nil {
		return err
	}
	for _, reference := range references {
		if reference == "" {
			continue
		}
		resolved, err := canonicalPlacement(reference)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			return err
		}
		if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("private credential and runtime paths must be outside the installation metadata directory")
		}
	}
	return nil
}

func metadataJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("metadata has trailing content")
	}
	return nil
}

// ValidateMetadataContents refuses to expose unrelated files through the journal
// mount. Plans and guided input contain typed metadata/file references only.
func ValidateMetadataContents(directory string, record Record) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("installation metadata contains a non-metadata entry")
		}
		if entry.Name() == "executor.lock" {
			if info.Size() != 0 {
				return fmt.Errorf("installation lock contains unexpected data")
			}
			continue
		}
		data, err := ReadPrivate(filepath.Join(directory, entry.Name()))
		if err != nil {
			return err
		}
		valid := false
		var value Record
		if metadataJSON(data, &value) == nil && value.ID == record.ID && value.ConfigDigest == record.ConfigDigest {
			valid = true
		}
		var plan Plan
		if metadataJSON(data, &plan) == nil && plan.InstallationID == record.ID && plan.Hash == plan.digest() {
			valid = true
		}
		if config, err := DecodeConfig(data); err == nil && Digest(config) == record.ConfigDigest {
			valid = true
		}
		clear(data)
		if !valid {
			return fmt.Errorf("move non-metadata files out of the installation journal before preparing an executor")
		}
	}
	return nil
}
