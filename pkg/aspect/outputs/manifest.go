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

// Parsing of Bazel runfiles manifests, as written by SourceManifestAction:
// https://github.com/bazelbuild/bazel/blob/master/src/main/java/com/google/devtools/build/lib/analysis/SourceManifestAction.java
//
// Each line is `<runfiles path> <target>`, where the target is one of:
//   - empty, for an empty file (e.g. a generated __init__.py);
//   - an absolute path to the backing file or directory;
//   - the raw link target of an unresolved symlink (ctx.actions.declare_symlink()), which may be
//     relative to the entry's own directory in the runfiles tree.
//
// Since Bazel 7.4.0 a line that starts with a space is escaped (see unescape).

package outputs

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

const (
	runfilesManifestSuffix = ".runfiles_manifest"
	runfilesDirSuffix      = ".runfiles"
)

// manifestEntry is a single parsed line of a runfiles manifest.
type manifestEntry struct {
	runfilesPath string
	target       string
}

// runfilesManifest is a parsed runfiles manifest.
type runfilesManifest struct {
	path    string
	entries []manifestEntry
	// sortedPaths holds every entry's runfiles path, sorted, for prefix lookups.
	sortedPaths []string
}

// readRunfilesManifest parses the manifest at manifestPath. Malformed lines are skipped with a
// warning on stderr. The returned error wraps fs.ErrNotExist if the manifest is missing.
func readRunfilesManifest(manifestPath string) (*runfilesManifest, error) {
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open runfiles manifest %s: %w", manifestPath, err)
	}
	defer f.Close()

	m := &runfilesManifest{path: manifestPath}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		entry, ok := parseManifestLine(scanner.Text())
		if !ok {
			fmt.Fprintf(os.Stderr, "skipping invalid runfiles manifest entry: '%s'\n", scanner.Text())
			continue
		}
		m.entries = append(m.entries, entry)
		m.sortedPaths = append(m.sortedPaths, entry.runfilesPath)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read runfiles manifest %s: %w", manifestPath, err)
	}
	sort.Strings(m.sortedPaths)
	return m, nil
}

// parseManifestLine parses one manifest line. The target is everything after the first space,
// so unescaped targets may contain spaces. Both fields are unescaped only on escaped lines.
func parseManifestLine(line string) (manifestEntry, bool) {
	escaped := strings.HasPrefix(line, " ")
	if escaped {
		line = line[1:]
	}
	runfilesPath, target, ok := strings.Cut(line, " ")
	if !ok || runfilesPath == "" {
		return manifestEntry{}, false
	}
	if escaped {
		runfilesPath, target = unescape(runfilesPath), unescape(target)
	}
	return manifestEntry{runfilesPath: runfilesPath, target: target}, true
}

// runfilesDir returns the runfiles tree directory next to a <exe>.runfiles_manifest, or "" for a
// manifest with any other name.
func (m *runfilesManifest) runfilesDir() string {
	if !strings.HasSuffix(m.path, runfilesManifestSuffix) {
		return ""
	}
	return strings.TrimSuffix(m.path, runfilesManifestSuffix) + runfilesDirSuffix
}

// covers reports whether runfilesPath is a manifest entry or a directory containing one. The
// runfiles root (".") covers every entry; paths outside the tree cover none.
func (m *runfilesManifest) covers(runfilesPath string) bool {
	if !isWithinRunfilesTree(runfilesPath) {
		return false
	}
	if runfilesPath == "." {
		return len(m.sortedPaths) > 0
	}
	i := sort.SearchStrings(m.sortedPaths, runfilesPath)
	if i < len(m.sortedPaths) && m.sortedPaths[i] == runfilesPath {
		return true
	}
	prefix := runfilesPath + "/"
	j := sort.SearchStrings(m.sortedPaths, prefix)
	return j < len(m.sortedPaths) && strings.HasPrefix(m.sortedPaths[j], prefix)
}

// isWithinRunfilesTree reports whether a clean, slash-separated runfiles path stays inside the
// runfiles tree.
func isWithinRunfilesTree(runfilesPath string) bool {
	return runfilesPath != ".." && !strings.HasPrefix(runfilesPath, "../") && !path.IsAbs(runfilesPath)
}

// linkedRunfilesPath returns the runfiles path a relative symlink target points at, resolved
// lexically against the entry's directory. The result may escape the runfiles tree.
func (e manifestEntry) linkedRunfilesPath() string {
	return path.Join(path.Dir(e.runfilesPath), e.target)
}
