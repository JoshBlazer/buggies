// Package skills is Buggies' per-bug-class knowledge: one SKILL.md per class,
// loaded only for the candidate it matches, so the agents don't carry every
// instruction in every prompt. The files are embedded so the single
// self-hosted binary needs nothing on disk.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/joshblazer/kernel/finding"
)

//go:embed */SKILL.md
var builtin embed.FS

// Skill is one bug class's recognition + reproduction guidance.
type Skill struct {
	Name        string
	Description string
	AppliesTo   finding.Type
	Body        string
}

// Set maps a finding type to the skill that covers it.
type Set map[finding.Type]Skill

// For returns the skill matching typ, if any. This is the selective-loading
// point: a candidate pulls in only its own class's skill.
func (s Set) For(typ finding.Type) (Skill, bool) {
	sk, ok := s[typ]
	return sk, ok
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	AppliesTo   string `yaml:"applies_to"`
}

// Load reads the embedded skill library.
func Load() (Set, error) { return loadFS(builtin) }

func loadFS(fsys fs.FS) (Set, error) {
	set := Set{}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "SKILL.md") {
			return err
		}
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		sk, err := parse(raw)
		if err != nil {
			return fmt.Errorf("skills: %s: %w", path, err)
		}
		if _, dup := set[sk.AppliesTo]; dup {
			return fmt.Errorf("skills: two skills for type %q", sk.AppliesTo)
		}
		set[sk.AppliesTo] = sk
		return nil
	})
	return set, err
}

// parse splits "---\n<yaml>\n---\n<body>" into a Skill.
func parse(raw []byte) (Skill, error) {
	s := string(raw)
	if !strings.HasPrefix(s, "---") {
		return Skill{}, fmt.Errorf("missing frontmatter")
	}
	rest := strings.TrimPrefix(s, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Skill{}, fmt.Errorf("unterminated frontmatter")
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return Skill{}, err
	}
	if fm.Name == "" || fm.Description == "" || fm.AppliesTo == "" {
		return Skill{}, fmt.Errorf("frontmatter needs name, description, applies_to")
	}
	body := strings.TrimSpace(rest[end+len("\n---"):])
	return Skill{
		Name:        fm.Name,
		Description: fm.Description,
		AppliesTo:   finding.Type(fm.AppliesTo),
		Body:        body,
	}, nil
}
