/*
 * Copyright 2023 Aspect Build Systems, Inc.
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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func testFixtures(num int) []string {
	r := make([]string, 0, num)
	for i := range num {
		r = append(r, fmt.Sprintf("test-fixture-%v", i))
	}
	return r
}

func TestHash(t *testing.T) {
	t.Run("hashMurmur3Sync and hashMurmur3Concurrent return the same hash for the same set of files", func(t *testing.T) {
		g := NewGomegaWithT(t)

		const testTarget1 = "//pkg/aspect/outputs:test_label"
		const testTarget2 = "//pkg/aspect/outputs:test_label"

		hashFiles := make(map[string][]string)
		hashFiles["//:test_label_1"] = testFixtures(1)
		hashFiles["//:test_label_5"] = testFixtures(5)
		hashFiles["//:test_label_9"] = testFixtures(9)

		resultSync, err := HashLabelFiles(hashFiles, 0, "")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync["//:test_label_1"]).To(Equal("m3:ZE71kCDqPq6GAqERC0yCeQ=="))
		g.Expect(resultSync["//:test_label_5"]).To(Equal("m3:yBvd/Ck4Gg7BlAQeFGu9iQ=="))
		g.Expect(resultSync["//:test_label_9"]).To(Equal("m3:QAVrLNyY6kHqeiwk0RV8+A=="))

		result1, err := HashLabelFiles(hashFiles, 1, "")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result1))

		result2, err := HashLabelFiles(hashFiles, 2, "")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result2))

		result10, err := HashLabelFiles(hashFiles, 10, "")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result10))

		result100, err := HashLabelFiles(hashFiles, 100, "")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result100))
	})

	t.Run("hashMurmur3Sync and hashMurmur3Concurrent return the same hash for the same set of files and salt", func(t *testing.T) {
		g := NewGomegaWithT(t)

		const testTarget1 = "//cli/core/pkg/aspect/outputs:test_label"
		const testTarget2 = "//cli/core/pkg/aspect/outputs:test_label"

		hashFiles := make(map[string][]string)
		hashFiles["//:test_label_1"] = testFixtures(1)
		hashFiles["//:test_label_5"] = testFixtures(5)
		hashFiles["//:test_label_9"] = testFixtures(9)

		resultSync, err := HashLabelFiles(hashFiles, 0, "some_salt")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync["//:test_label_1"]).To(Equal("m3:Hxij54nvy33XcEAi61+T/A=="))
		g.Expect(resultSync["//:test_label_5"]).To(Equal("m3:rjtVewacPu6iii2Tmlp7tw=="))
		g.Expect(resultSync["//:test_label_9"]).To(Equal("m3:ldAf2XzRElH7xV3m2ZpgOA=="))

		result1, err := HashLabelFiles(hashFiles, 1, "some_salt")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result1))

		result2, err := HashLabelFiles(hashFiles, 2, "some_salt")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result2))

		result10, err := HashLabelFiles(hashFiles, 10, "some_salt")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result10))

		result100, err := HashLabelFiles(hashFiles, 100, "some_salt")
		g.Expect(err).To(BeNil())
		g.Expect(resultSync).To(Equal(result100))
	})
}

// writeFiles creates each file (and its parent directories) with the given content.
func writeFiles(g *WithT, files map[string]string) {
	for p, content := range files {
		g.Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		g.Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
	}
}

func TestAddRunfilesHash(t *testing.T) {
	t.Run("handles empty files, escaped lines and declare_symlink entries", func(t *testing.T) {
		g := NewGomegaWithT(t)
		tmp := t.TempDir()
		outputBase := filepath.Join(tmp, "output_base")
		python := filepath.Join(outputBase, "external", "rules_python++python+python_3_11", "bin", "python3")
		spaced := filepath.Join(tmp, "a b.txt")
		backslashed := filepath.Join(tmp, `c\sd.txt`)
		manifest := filepath.Join(tmp, "bin", "multirun.runfiles_manifest")
		writeFiles(g, map[string]string{
			python:      "x",
			spaced:      "x",
			backslashed: "x",
			manifest: "__init__.py \n" +
				"rules_multirun+/internal/__init__.py \n" +
				` _main/a\sb.txt ` + spaced + "\n" +
				"_main/c.txt " + backslashed + "\n" +
				"rules_python++python+python_3_11/bin/python3 " + python + "\n" +
				"rules_multirun+/internal/_multirun.venv/bin/python3 ../../../../rules_python++python+python_3_11/bin/python3\n" +
				"rules_multirun+/internal/_multirun.venv/bin/python ../../../../rules_python++python+python_3_11/bin\n" +
				"rules_multirun+/internal/_multirun.venv/bin/dangling ../missing\n",
		})

		hashFiles := make(map[string][]string)
		g.Expect(AddRunfilesHash(hashFiles, "//:multirun", manifest, outputBase)).To(Succeed())
		g.Expect(hashFiles["//:multirun"]).To(ConsistOf(spaced, backslashed, python))
	})

	t.Run("resolves relative targets under the output base's external directory", func(t *testing.T) {
		g := NewGomegaWithT(t)
		tmp := t.TempDir()
		target := filepath.Join(tmp, "external", "repo", "file")
		manifest := filepath.Join(tmp, "tool.runfiles_manifest")
		writeFiles(g, map[string]string{target: "x", manifest: "_main/link repo/file\n"})

		hashFiles := make(map[string][]string)
		g.Expect(AddRunfilesHash(hashFiles, "//:tool", manifest, tmp)).To(Succeed())
		g.Expect(hashFiles["//:tool"]).To(ConsistOf(target))
	})

	t.Run("resolves symlinks to files missing from the manifest via the runfiles tree", func(t *testing.T) {
		g := NewGomegaWithT(t)
		tmp := t.TempDir()
		target := filepath.Join(tmp, "bin", "tool.runfiles", "other", "file")
		manifest := filepath.Join(tmp, "bin", "tool.runfiles_manifest")
		writeFiles(g, map[string]string{target: "x", manifest: "_main/link ../other/file\n"})

		hashFiles := make(map[string][]string)
		g.Expect(AddRunfilesHash(hashFiles, "//:tool", manifest, filepath.Join(tmp, "ob"))).To(Succeed())
		g.Expect(hashFiles["//:tool"]).To(ConsistOf(target))
	})

	t.Run("hashes directory targets recursively", func(t *testing.T) {
		g := NewGomegaWithT(t)
		tmp := t.TempDir()
		dir := filepath.Join(tmp, "dir")
		manifest := filepath.Join(tmp, "tool.runfiles_manifest")
		writeFiles(g, map[string]string{
			filepath.Join(dir, "a"):        "a",
			filepath.Join(dir, "sub", "b"): "b",
			manifest:                       "_main/dir " + dir + "\n",
		})

		hashFiles := make(map[string][]string)
		g.Expect(AddRunfilesHash(hashFiles, "//:tool", manifest, tmp)).To(Succeed())
		g.Expect(hashFiles["//:tool"]).To(ConsistOf(filepath.Join(dir, "a"), filepath.Join(dir, "sub", "b")))
	})

	t.Run("skips a missing manifest", func(t *testing.T) {
		g := NewGomegaWithT(t)
		tmp := t.TempDir()

		hashFiles := make(map[string][]string)
		g.Expect(AddRunfilesHash(hashFiles, "//:tool", filepath.Join(tmp, "tool.runfiles_manifest"), tmp)).To(Succeed())
		g.Expect(hashFiles).To(BeEmpty())
	})

	t.Run("fails on a missing absolute target", func(t *testing.T) {
		g := NewGomegaWithT(t)
		tmp := t.TempDir()
		manifest := filepath.Join(tmp, "tool.runfiles_manifest")
		writeFiles(g, map[string]string{manifest: "_main/f " + filepath.Join(tmp, "nope") + "\n"})

		g.Expect(AddRunfilesHash(map[string][]string{}, "//:tool", manifest, tmp)).NotTo(Succeed())
	})
}
