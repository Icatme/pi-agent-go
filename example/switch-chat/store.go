package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const registryFileName = "registry.json"

func loadRegistry(dataDir string) (RegistryState, error) {
	path := registryPath(dataDir)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RegistryState{
			Sessions: map[string]SessionRecord{},
		}, nil
	}
	if err != nil {
		return RegistryState{}, err
	}

	var state RegistryState
	if err := json.Unmarshal(body, &state); err != nil {
		return RegistryState{}, err
	}
	if state.Sessions == nil {
		state.Sessions = map[string]SessionRecord{}
	}
	return state, nil
}

func saveRegistry(dataDir string, state RegistryState) error {
	if state.Sessions == nil {
		state.Sessions = map[string]SessionRecord{}
	}

	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(registryPath(dataDir), body, 0o644)
}

func registryPath(dataDir string) string {
	return filepath.Join(dataDir, registryFileName)
}
