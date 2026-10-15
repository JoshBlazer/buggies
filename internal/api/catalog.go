package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/joshblazer/buggies/internal/store"
)

// DirCatalog is the filesystem Catalog: per-repo finding stores live under
// <Root>/<owner>__<repo>/findings.db, exactly the layout cmd/buggies serve
// writes (dataDir(name) replaces "/" with "__"). It decrypts with the process's
// BUGGIES_STORE_KEY or each dir's .buggies-store.key, the same key the review
// path already holds.
type DirCatalog struct{ Root string }

func (c DirCatalog) Repos() ([]string, error) {
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.Root, e.Name(), "findings.db")); err != nil {
			continue
		}
		out = append(out, dirToRepo(e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

func (c DirCatalog) Open(repo string) (FindingStore, func() error, error) {
	dsn := filepath.Join(c.Root, repoToDir(repo), "findings.db")
	st, err := store.Open(dsn)
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return st, st.Close, nil
}

func repoToDir(repo string) string { return strings.ReplaceAll(repo, "/", "__") }
func dirToRepo(dir string) string  { return strings.ReplaceAll(dir, "__", "/") }

// FileSettings persists AdminSettings as a JSON file under the data root. On a
// missing file it returns Defaults (seeded from BUGGIES_BUDGET_CENTS at
// startup), so the budget cap survives a restart as a live override the
// Reviewer.Budget closure reads per-call. Concurrency-safe for the daemon's
// handler goroutines.
type FileSettings struct {
	Path     string
	Defaults AdminSettings
	mu       sync.Mutex
}

type settingsFile struct {
	BudgetCents   int    `json:"budgetCents"`
	MinConfidence string `json:"minConfidence"`
	PostAdvisory  bool   `json:"postAdvisory"`
}

func (f *FileSettings) Get() (AdminSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := os.ReadFile(f.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return f.Defaults, nil
		}
		return AdminSettings{}, err
	}
	var sf settingsFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return AdminSettings{}, err
	}
	s := AdminSettings{BudgetCents: sf.BudgetCents, PostAdvisory: sf.PostAdvisory, MinConfidence: f.Defaults.MinConfidence}
	if conf, ok := parseConfidence(sf.MinConfidence); ok {
		s.MinConfidence = conf
	}
	return s, nil
}

func (f *FileSettings) Set(s AdminSettings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := json.MarshalIndent(settingsFile{
		BudgetCents:   s.BudgetCents,
		MinConfidence: s.MinConfidence.String(),
		PostAdvisory:  s.PostAdvisory,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.Path, append(b, '\n'), 0o600)
}

// compile-time assertions that the concrete types satisfy the API interfaces.
var (
	_ Catalog       = DirCatalog{}
	_ SettingsStore = (*FileSettings)(nil)
	_ FindingStore  = (*store.Store)(nil)
)
