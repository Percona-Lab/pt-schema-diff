package schemadiff

// Snapshots: a schema saved at a point in time, to compare against later (e.g. to see what changed on a server since
// last week). One JSON file per snapshot.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Snapshot is a saved schema.
type Snapshot struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Label     string            `json:"label"`
	Database  string            `json:"database"`
	Created   string            `json:"created"`
	CreatedBy string            `json:"created_by"`
	Tables    map[string]string `json:"tables"`
	Sizes     Sizes             `json:"sizes"`
}

// SnapshotInfo summarizes a snapshot.
type SnapshotInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	Database  string `json:"database"`
	Created   string `json:"created"`
	CreatedBy string `json:"created_by"`
	Tables    int    `json:"tables"`
}

var reSnapshotID = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

// SnapshotStore keeps snapshots in a directory.
type SnapshotStore struct{ Dir string }

func (s SnapshotStore) path(id string) (string, error) {
	if !reSnapshotID.MatchString(id) {
		return "", fmt.Errorf("invalid snapshot id %q (expected e.g. 20260101-120000-a1b2c3)", id)
	}
	return filepath.Join(s.Dir, id+".json"), nil
}

// Save writes a new snapshot and returns its id.
func (s SnapshotStore) Save(name, label, database string, tables map[string]string, sizes Sizes, user string) (string, error) {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	now := Now()
	id := now.Format("20060102-150405") + "-" + hex.EncodeToString(suffix)
	if strings.TrimSpace(name) == "" {
		name = label
	}
	if sizes == nil {
		sizes = Sizes{}
	}
	data, err := json.MarshalIndent(Snapshot{ID: id, Name: strings.TrimSpace(name), Label: label, Database: database,
		Created: now.Format("2006-01-02T15:04:05"), CreatedBy: user, Tables: tables, Sizes: sizes}, "", " ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	path, _ := s.path(id)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	return id, os.Rename(tmp, path)
}

// List returns snapshot summaries, newest first.
func (s SnapshotStore) List() ([]SnapshotInfo, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json") && reSnapshotID.MatchString(strings.TrimSuffix(n, ".json")) {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var out []SnapshotInfo
	for _, n := range names {
		snap, err := s.Load(strings.TrimSuffix(n, ".json"))
		if err != nil {
			continue
		}
		out = append(out, SnapshotInfo{ID: snap.ID, Name: snap.Name, Label: snap.Label, Database: snap.Database,
			Created: snap.Created, CreatedBy: snap.CreatedBy, Tables: len(snap.Tables)})
	}
	return out, nil
}

// Load reads a snapshot.
func (s SnapshotStore) Load(id string) (*Snapshot, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", id, err)
	}
	if snap.Tables == nil {
		snap.Tables = map[string]string{}
	}
	return &snap, nil
}

// Delete removes a snapshot.
func (s SnapshotStore) Delete(id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
