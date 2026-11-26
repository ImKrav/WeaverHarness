package run

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Store persists run states and agent artifacts under <repo>/.weaver/runs.
type Store struct {
	Root string
}

// Save atomically persists s to <Root>/<RunID>/state.json.
func (st Store) Save(s *State) error {
	if s == nil || s.RunID == "" {
		return errors.New("run: estado sin RunID")
	}
	runDir := filepath.Join(st.Root, s.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("run: creando directorio de ejecución: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("run: serializando estado: %w", err)
	}

	tmp, err := os.CreateTemp(runDir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("run: creando archivo temporal: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("run: escribiendo estado temporal: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("run: cerrando estado temporal: %w", err)
	}

	target := filepath.Join(runDir, "state.json")
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("run: renombrando estado: %w", err)
	}
	cleanup = false
	return nil
}

// Load reads and decodes the persisted state for runID.
func (st Store) Load(runID string) (*State, error) {
	target := filepath.Join(st.Root, runID, "state.json")
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("run: leyendo %s: %w", target, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("run: decodificando %s: %w", target, err)
	}
	return &s, nil
}

// List returns all saved runs ordered ascending by RunID.
func (st Store) List() ([]*State, error) {
	entries, err := os.ReadDir(st.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []*State{}, nil
		}
		return nil, fmt.Errorf("run: listando %s: %w", st.Root, err)
	}

	runs := []*State{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		s, err := st.Load(entry.Name())
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		runs = append(runs, s)
	}

	slices.SortFunc(runs, func(a, b *State) int {
		return cmp.Compare(a.RunID, b.RunID)
	})
	return runs, nil
}

// Latest returns the newest saved run by RunID order.
func (st Store) Latest() (*State, error) {
	runs, err := st.List()
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("run: no hay ejecuciones guardadas: %w", fs.ErrNotExist)
	}
	return runs[len(runs)-1], nil
}

// Delete removes the persisted directory for runID.
func (st Store) Delete(runID string) error {
	return os.RemoveAll(filepath.Join(st.Root, runID))
}

// AgentDir returns the artifact directory for an agent within runID.
func (st Store) AgentDir(runID, agentID string) string {
	return filepath.Join(st.Root, runID, "agents", agentID)
}
