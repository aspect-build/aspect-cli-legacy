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

// For copybara, this import comes alphabetically before aspect.build
// and causes the line ordering in the import to change.
// So we just import it in a separate block.
import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphadose/haxmap"
	"github.com/aspect-build/aspect-cli-legacy/pkg/bazel"
	"golang.org/x/mod/sumdb/dirhash"

	concurrently "github.com/tejzpr/ordered-concurrently/v3"
	"github.com/twmb/murmur3"
)

const numConcurrentHashingThreads = 4

// AddExecutableHash appends the exePath to hashFiles entry of the label
func AddExecutableHash(hashFiles map[string][]string, label string, exePath string) {
	_, err := os.Stat(exePath)
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "%s output %s is not on disk, did you build it? Skipping...\n", label, exePath)
		return
	}

	hashFiles[label] = append(hashFiles[label], exePath)
}

// manifestEntry is a single parsed line of a runfiles manifest.
type manifestEntry struct {
	// runfilesPath is the path of the entry inside the runfiles tree.
	runfilesPath string
	// target is the file the entry points to. It is empty for empty files (e.g. generated
	// __init__.py files) and, for unresolved symlinks created with ctx.actions.declare_symlink(),
	// it is the raw link target, which is relative to the entry's directory in the runfiles tree.
	target string
}

// parseManifestLine parses a runfiles manifest line. Lines are of the form
//
//	path/in/runfiles /some/absolute/path/to/actual/file
//	path/in/runfiles some/relative/path/to/actual/file
//	path/in/runfiles
//
// where the last form (trailing space, empty target) denotes an empty file. Since Bazel 7.4.0 a
// line starting with a space has an escaped runfiles path (and target); see unescape.
func parseManifestLine(rawLine string) (manifestEntry, bool) {
	line := strings.TrimRight(rawLine, "\r")
	escaped := strings.HasPrefix(line, " ")
	if escaped {
		line = line[1:]
	}
	runfilesPath, target, ok := strings.Cut(line, " ")
	if !ok || runfilesPath == "" {
		return manifestEntry{}, false
	}
	if escaped {
		runfilesPath = unescape(runfilesPath)
	}
	// Everything after the first space is the target, including additional unescaped spaces.
	return manifestEntry{runfilesPath: runfilesPath, target: unescape(target)}, true
}

// AddRunfilesHash iterates through the runfiles entries of the manifest, appending all files
// contained (or files inside directories) to the hashFiles entry of the label.
//
// Empty-file entries are skipped. Relative targets are resolved against outputBase/external,
// then the working directory, and finally as unresolved symlinks relative to the entry's
// directory in the runfiles tree. A symlink that points at another manifest entry (or a directory
// containing manifest entries) is skipped since its target is hashed via that entry; one that
// cannot be resolved at all is skipped with a warning rather than failing.
func AddRunfilesHash(hashFiles map[string][]string, label string, manifestPath string, outputBase string) error {
	_, err := os.Stat(manifestPath)
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "%s manifest %s is not on disk, did you build it? Skipping...\n", label, manifestPath)
		return nil
	}
	runfiles, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to open runfiles manifest %s: %w\n", manifestPath, err)
	}
	defer runfiles.Close()

	var entries []manifestEntry
	runfilesPaths := make(map[string]struct{})
	fileScanner := bufio.NewScanner(runfiles)
	fileScanner.Split(bufio.ScanLines)
	for fileScanner.Scan() {
		rawLine := fileScanner.Text()
		entry, ok := parseManifestLine(rawLine)
		if !ok {
			fmt.Fprintf(os.Stderr, "skipping invalid runfiles manifest entry: '%s'\n", rawLine)
			continue
		}
		entries = append(entries, entry)
		runfilesPaths[entry.runfilesPath] = struct{}{}
	}
	if err := fileScanner.Err(); err != nil {
		return fmt.Errorf("failed to read runfiles manifest %s: %w\n", manifestPath, err)
	}

	runfilesDir := strings.TrimSuffix(manifestPath, "_manifest")

	for _, entry := range entries {
		if entry.target == "" {
			continue
		}
		actualPath, fileinfo, err := statManifestTarget(entry.target, outputBase)
		if err != nil && !filepath.IsAbs(entry.target) {
			linked := path.Clean(path.Join(path.Dir(entry.runfilesPath), entry.target))
			if isWithinRunfiles(linked) && coversManifestEntry(linked, runfilesPaths, entries) {
				continue
			}
			inTree := filepath.Join(runfilesDir, filepath.FromSlash(linked))
			if fi, treeErr := os.Stat(inTree); treeErr == nil {
				actualPath, fileinfo, err = inTree, fi, nil
			} else {
				fmt.Fprintf(os.Stderr, "skipping unresolvable runfiles symlink %s -> %s\n", entry.runfilesPath, entry.target)
				continue
			}
		}

		if err != nil {
			return fmt.Errorf("failed to stat runfiles manifest entry %s: %w\n", actualPath, err)
		}

		if fileinfo.IsDir() {
			// TODO(alexeagle): I think the actualPath means we'll get more hashed than we mean to
			// we should pass some other value to the second arg "prefix"
			direntries, err := dirhash.DirFiles(actualPath, actualPath)
			if err != nil {
				return fmt.Errorf("failed to recursively list directory %s: %w\n", actualPath, err)
			}
			hashFiles[label] = append(hashFiles[label], direntries...)
		} else {
			hashFiles[label] = append(hashFiles[label], actualPath)
		}
	}
	return nil
}

// statManifestTarget stats a manifest target and returns the path it was found at. As of Bazel
// 7.4.0 targets can be relative; those are tried under outputBase/external first, then relative
// to the working directory.
func statManifestTarget(target string, outputBase string) (string, fs.FileInfo, error) {
	if !filepath.IsAbs(target) {
		external := filepath.Join(outputBase, "external", target)
		if fi, err := os.Stat(external); err == nil {
			return external, fi, nil
		}
	}
	fi, err := os.Stat(target)
	return target, fi, err
}

// isWithinRunfiles reports whether a cleaned, slash-separated runfiles path stays inside the
// runfiles tree.
func isWithinRunfiles(p string) bool {
	return p != "." && p != ".." && !strings.HasPrefix(p, "../") && !path.IsAbs(p)
}

// coversManifestEntry reports whether runfilesPath is itself a manifest entry or a directory
// containing one.
func coversManifestEntry(runfilesPath string, runfilesPaths map[string]struct{}, entries []manifestEntry) bool {
	if _, ok := runfilesPaths[runfilesPath]; ok {
		return true
	}
	prefix := runfilesPath + "/"
	for _, e := range entries {
		if strings.HasPrefix(e.runfilesPath, prefix) {
			return true
		}
	}
	return false
}

// =================================================================================================
func gatherExecutableHashes(outs []bazel.Output, salt string, outputBase string) (map[string]string, error) {
	// map from Label to the files/directories which should be hashed
	hashFiles := make(map[string][]string)

	for _, a := range outs {
		if a.Mnemonic == "ExecutableSymlink" {
			AddExecutableHash(hashFiles, a.Label, a.Path)
		} else if a.Mnemonic == "SourceSymlinkManifest" {
			if err := AddRunfilesHash(hashFiles, a.Label, a.Path, outputBase); err != nil {
				return nil, err
			}
		}
	}

	return HashLabelFiles(hashFiles, numConcurrentHashingThreads, salt)
}

func HashLabelFiles(labelFiles map[string][]string, concurrency int, salt string) (map[string]string, error) {
	// cache of file hashes so we don't hash the same file twice for different targets
	mep := haxmap.New[string, string]()
	result := make(map[string]string)
	for label, files := range labelFiles {
		var hash string
		var err error
		if concurrency == 0 {
			// Fully synchronous hash implementation is used for testing to ensure that the faster
			// concurrent implementation generates an identical hash to the slower sync implementation.
			hash, err = hashMurmur3Sync(files, mep, salt)
		} else {
			hash, err = hashMurmur3Concurrent(files, mep, concurrency, salt)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to compute runfiles hash for manifest: %w\n", err)
		}
		result[label] = hash
	}
	return result, nil
}

// =================================================================================================
// https://github.com/twmb/murmur3
// =================================================================================================
func hashMurmur3Sync(files []string, mep *haxmap.Map[string, string], salt string) (string, error) {
	h := murmur3.New128()
	files = append([]string(nil), files...)
	sort.Strings(files)
	for _, file := range files {
		s, ok := mep.Get(file)
		if ok {
			h.Write([]byte(s))
			continue
		}
		if strings.Contains(file, "\n") {
			return "", errors.New("filenames with newlines are not supported")
		}
		r, err := os.Open(file)
		if err != nil {
			return "", err
		}
		hf := murmur3.New128()
		_, err = io.Copy(hf, r)
		r.Close()
		if err != nil {
			return "", err
		}
		s = fmt.Sprintf("%x  %s\n", hf.Sum(nil), file)
		mep.Set(file, s)
		h.Write([]byte(s))
	}
	h.Write([]byte(salt))
	return "m3:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// =================================================================================================
// https://github.com/twmb/murmur3 + https://github.com/tejzpr/ordered-concurrently
// =================================================================================================
type cachedHashResult struct {
	file   string
	result string
}

type hashResult struct {
	file   string
	result string
	err    error
}

func hashMurmur3Concurrent(files []string, mep *haxmap.Map[string, string], numThreads int, salt string) (string, error) {
	h := murmur3.New128()
	files = append([]string(nil), files...)
	sort.Strings(files)
	// https://github.com/tejzpr/ordered-concurrently#example---1
	inputChan := make(chan concurrently.WorkFunction)
	ctx := context.Background()
	output := concurrently.Process(ctx, inputChan, &concurrently.Options{PoolSize: numThreads, OutChannelBuffer: len(files)})
	maybeCached := make([]cachedHashResult, 0, len(files))
	for _, file := range files {
		s, ok := mep.Get(file)
		if ok {
			maybeCached = append(maybeCached, cachedHashResult{
				file:   file,
				result: s,
			})
		} else {
			maybeCached = append(maybeCached, cachedHashResult{
				file: file,
			})
		}
	}
	go func() {
		for _, m := range maybeCached {
			if m.result != "" {
				inputChan <- cachedPassThrough(cachedHashResult{
					file:   m.file,
					result: m.result,
				})
			} else {
				inputChan <- hashWorker(m.file)
			}
		}
		close(inputChan)
	}()
	for out := range output {
		if chr, ok := out.Value.(cachedHashResult); ok {
			h.Write([]byte(chr.result))
		} else if chr, ok := out.Value.(hashResult); ok {
			if chr.err != nil {
				return "", fmt.Errorf("error concurrently hashing file %v: %w", chr.file, chr.err)
			}
			mep.Set(chr.file, chr.result)
			h.Write([]byte(chr.result))
		} else {
			return "", fmt.Errorf("expected go routine to return a cachedHashResult or hashResult")
		}
	}
	h.Write([]byte(salt))
	return "m3:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

type cachedPassThrough cachedHashResult

func (i cachedPassThrough) Run(ctx context.Context) any {
	return cachedHashResult{
		file:   i.file,
		result: i.result,
	}
}

type hashWorker string

func (i hashWorker) Run(ctx context.Context) any {
	file := string(i)
	if strings.Contains(file, "\n") {
		return hashResult{
			file: file,
			err:  fmt.Errorf("filenames with newlines are not supported"),
		}
	}
	r, err := os.Open(file)
	if err != nil {
		return hashResult{
			file: file,
			err:  fmt.Errorf("failed to open file %v for hashing: %w", file, err),
		}
	}
	hf := murmur3.New128()
	_, err = io.Copy(hf, r)
	r.Close()
	if err != nil {
		return hashResult{
			file: file,
			err:  fmt.Errorf("failed to stream file %v for hashing: %w", file, err),
		}
	}
	return hashResult{
		file:   file,
		result: fmt.Sprintf("%x  %s\n", hf.Sum(nil), file),
	}
}
