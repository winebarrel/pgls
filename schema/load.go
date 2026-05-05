package schema

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Load(dir string) (*Schema, error) {
	merged := &Schema{Tables: map[string]*Table{}}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".sql") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s, err := Parse(string(b))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for name, t := range s.Tables {
			merged.Tables[name] = t
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return merged, nil
}
