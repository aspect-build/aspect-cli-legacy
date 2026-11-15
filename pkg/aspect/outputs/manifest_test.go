/*
 * Copyright 2026 Aspect Build Systems, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package outputs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestParseManifestLine(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		entry manifestEntry
		ok    bool
	}{
		{"absolute target", "_main/a /abs/a", manifestEntry{"_main/a", "/abs/a"}, true},
		{"empty file", "__init__.py ", manifestEntry{"__init__.py", ""}, true},
		{"relative symlink", "x/bin/py ../../y/py", manifestEntry{"x/bin/py", "../../y/py"}, true},
		{"unescaped target keeps spaces", "_main/a /abs/a b", manifestEntry{"_main/a", "/abs/a b"}, true},
		{"unescaped target keeps backslashes", `_main/a /abs/a\sb`, manifestEntry{"_main/a", `/abs/a\sb`}, true},
		{"escaped line", ` _main/a\sb\bc /abs/a b\bc`, manifestEntry{`_main/a b\c`, `/abs/a b\c`}, true},
		{"escaped empty file", ` _main/a\sb `, manifestEntry{"_main/a b", ""}, true},
		{"no separator", "_main/a", manifestEntry{}, false},
		{"empty line", "", manifestEntry{}, false},
		{"lone space", " ", manifestEntry{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			entry, ok := parseManifestLine(tt.line)
			g.Expect(ok).To(Equal(tt.ok))
			g.Expect(entry).To(Equal(tt.entry))
		})
	}
}

func TestReadRunfilesManifest(t *testing.T) {
	t.Run("skips invalid lines and sorts paths", func(t *testing.T) {
		g := NewGomegaWithT(t)
		p := filepath.Join(t.TempDir(), "x.runfiles_manifest")
		g.Expect(os.WriteFile(p, []byte("b /b\ninvalid\na \n"), 0o644)).To(Succeed())

		m, err := readRunfilesManifest(p)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(m.entries).To(Equal([]manifestEntry{{"b", "/b"}, {"a", ""}}))
		g.Expect(m.sortedPaths).To(Equal([]string{"a", "b"}))
	})

	t.Run("wraps fs.ErrNotExist for a missing manifest", func(t *testing.T) {
		g := NewGomegaWithT(t)
		_, err := readRunfilesManifest(filepath.Join(t.TempDir(), "missing"))
		g.Expect(errors.Is(err, fs.ErrNotExist)).To(BeTrue())
	})
}

func TestRunfilesDir(t *testing.T) {
	g := NewGomegaWithT(t)
	g.Expect((&runfilesManifest{path: "/out/bin/tool.runfiles_manifest"}).runfilesDir()).To(Equal("/out/bin/tool.runfiles"))
	g.Expect((&runfilesManifest{path: "/out/bin/tool.manifest"}).runfilesDir()).To(Equal(""))
}

func TestCovers(t *testing.T) {
	t.Run("empty manifest covers nothing", func(t *testing.T) {
		NewGomegaWithT(t).Expect((&runfilesManifest{}).covers(".")).To(BeFalse())
	})

	m := &runfilesManifest{sortedPaths: []string{"a/b/c", "a/bb", "d"}}
	tests := []struct {
		path    string
		covered bool
	}{
		{"d", true},
		{"a/b/c", true},
		{"a/b", true},
		{"a", true},
		{"a/bb", true},
		{"a/b/c/d", false},
		{"a/ba", false},
		{"e", false},
		{".", true},
		{"..", false},
		{"../d", false},
		{"/d", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			NewGomegaWithT(t).Expect(m.covers(tt.path)).To(Equal(tt.covered))
		})
	}
}

func TestLinkedRunfilesPath(t *testing.T) {
	g := NewGomegaWithT(t)
	g.Expect(manifestEntry{"r/venv/bin/python3", "../../../../py/bin/python3"}.linkedRunfilesPath()).To(Equal("../py/bin/python3"))
	g.Expect(manifestEntry{"r/venv/bin/python3", "../../../py/bin/python3"}.linkedRunfilesPath()).To(Equal("py/bin/python3"))
	g.Expect(manifestEntry{"r/bin/python", "python3"}.linkedRunfilesPath()).To(Equal("r/bin/python3"))
}

func TestIsWithinRunfilesTree(t *testing.T) {
	g := NewGomegaWithT(t)
	for _, p := range []string{".", "a", "a/b", "..a", "a/.."} {
		g.Expect(isWithinRunfilesTree(p)).To(BeTrue(), p)
	}
	for _, p := range []string{"..", "../a", "/a"} {
		g.Expect(isWithinRunfilesTree(p)).To(BeFalse(), p)
	}
}
