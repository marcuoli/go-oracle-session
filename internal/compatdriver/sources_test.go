package compatdriver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRetainedSources checks both integrity and completeness of the module-owned
// implementation. Nested modules would silently disappear from a Go module ZIP.
func TestRetainedSources(t *testing.T) {
	data, err := os.ReadFile("sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) == 0 {
		t.Fatal("empty retained-source manifest")
	}
	found := make(map[string]bool)
	for _, root := range []string{"v2", "v3"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				t.Errorf("redirecting retained source %s", path)
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Name() == "go.mod" || entry.Name() == "go.sum" {
				t.Errorf("nested module metadata %s", path)
			}
			path = filepath.ToSlash(path)
			record, ok := manifest.Files[path]
			if !ok {
				t.Errorf("unrecorded retained source %s", path)
				return nil
			}
			data, err := os.ReadFile(filepath.FromSlash(path))
			if err != nil {
				return err
			}
			sum := sha256.Sum256([]byte(strings.ReplaceAll(string(data), "\r\n", "\n")))
			if hex.EncodeToString(sum[:]) != record.SHA256 {
				t.Errorf("retained source changed: %s", path)
			}
			found[path] = true
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for path := range manifest.Files {
		if !found[path] {
			t.Errorf("missing retained source %s", path)
		}
	}
}
