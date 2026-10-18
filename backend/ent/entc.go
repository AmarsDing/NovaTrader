//go:build ignore

// Generate into staging and rename complete files. Windows source indexers can
// keep mapped sections open, which prevents the default generator truncation.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	stage, err := os.MkdirTemp("../runtime", "entgen-")
	must(err)
	defer os.RemoveAll(stage)
	must(entc.Generate("./schema", &gen.Config{Target: stage, Package: "server/ent", Features: []gen.Feature{gen.FeatureUpsert, gen.FeatureLock}}))
	must(filepath.WalkDir(stage, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		target := filepath.Join(".", rel)
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.Rename(path, target)
	}))
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
