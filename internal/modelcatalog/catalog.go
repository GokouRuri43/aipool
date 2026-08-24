package modelcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/local/aipool/internal/api"
)

type Entry struct {
	api.LocalModel
	Path string `json:"-"`
}

type Catalog struct {
	entries map[string]Entry
}

// Load scans GGUF files in dir. Overrides use the form public-name=path and
// allow stable model IDs independent of local filenames.
func Load(dir, overrides string) (*Catalog, error) {
	paths := map[string]string{}
	if dir != "" {
		matches, err := filepath.Glob(filepath.Join(dir, "*.gguf"))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			paths[id] = path
		}
	}
	for _, item := range strings.Split(overrides, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, path, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("invalid model mapping %q; expected name=path", item)
		}
		paths[strings.TrimSpace(id)] = strings.TrimSpace(path)
	}
	catalog := &Catalog{entries: make(map[string]Entry, len(paths))}
	for id, path := range paths {
		entry, err := inspect(id, path)
		if err != nil {
			return nil, fmt.Errorf("inspect model %q: %w", id, err)
		}
		catalog.entries[id] = entry
	}
	return catalog, nil
}

func (c *Catalog) Lookup(id string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	entry, ok := c.entries[id]
	return entry, ok
}

func (c *Catalog) Entries() []Entry {
	if c == nil {
		return nil
	}
	entries := make([]Entry, 0, len(c.entries))
	for _, entry := range c.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries
}

func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

func inspect(id, path string) (Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Entry{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return Entry{}, fmt.Errorf("model must be a non-empty regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return Entry{}, err
	}
	return Entry{LocalModel: api.LocalModel{ID: id, Digest: hex.EncodeToString(hash.Sum(nil)), Size: info.Size(), Format: "gguf"}, Path: path}, nil
}
