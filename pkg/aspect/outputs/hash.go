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
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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

// externalDir is the directory under the output base that holds external repositories.
const externalDir = "external"

// AddExecutableHash appends the exePath to hashFiles entry of the label
func AddExecutableHash(hashFiles map[string][]string, label string, exePath string) {
	_, err := os.Stat(exePath)
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "%s output %s is not on disk, did you build it? Skipping...\n", label, exePath)
		return
	}

	hashFiles[label] = append(hashFiles[label], exePath)
}

// AddRunfilesHash appends the files backing each entry of the runfiles manifest (recursing into
// directories) to the hashFiles entry of the label. A missing manifest is skipped with a warning.
//
// Empty-file entries are not hashed. Relative targets are unresolved symlinks; see
// resolveManifestTarget for how they are handled.
func AddRunfilesHash(hashFiles map[string][]string, label string, manifestPath string, outputBase string) error {
	manifest, err := readRunfilesManifest(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "%s manifest %s is not on disk, did you build it? Skipping...\n", label, manifestPath)
		return nil
	}
	if err != nil {
		return err
	}

	for _, entry := range manifest.entries {
		if entry.target == "" {
			continue
		}
		actualPath, fileinfo, err := resolveManifestTarget(manifest, entry, outputBase)
		if err != nil {
			return fmt.Errorf("failed to stat runfiles manifest entry %s: %w\n", entry.target, err)
		}
		if actualPath == "" {
			continue
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

// resolveManifestTarget returns the on-disk path and file info backing a non-empty manifest
// entry, or an empty path if the entry should not be hashed on its own.
//
// An absolute target must exist. A relative target is an unresolved symlink. If it points at
// another manifest entry (or a directory containing one) it is skipped, because that entry is
// hashed itself. Otherwise the first existing candidate wins:
//   - <outputBase>/external/<target>, if target doesn't escape that directory
//   - the link resolved inside the runfiles tree on disk, if it stays inside the tree
//   - <target> relative to the working directory, if target doesn't escape it
//
// A symlink that resolves to none of these is skipped with a warning.
func resolveManifestTarget(m *runfilesManifest, entry manifestEntry, outputBase string) (string, fs.FileInfo, error) {
	if filepath.IsAbs(entry.target) {
		fi, err := os.Stat(entry.target)
		return entry.target, fi, err
	}
	linked := entry.linkedRunfilesPath()
	if m.covers(linked) {
		return "", nil, nil
	}
	var candidates []string
	local := filepath.IsLocal(entry.target)
	if local {
		candidates = append(candidates, filepath.Join(outputBase, externalDir, entry.target))
	}
	if dir := m.runfilesDir(); dir != "" && isWithinRunfilesTree(linked) {
		candidates = append(candidates, filepath.Join(dir, filepath.FromSlash(linked)))
	}
	if local {
		candidates = append(candidates, entry.target)
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil {
			return c, fi, nil
		}
	}
	fmt.Fprintf(os.Stderr, "skipping unresolvable runfiles symlink %s -> %s\n", entry.runfilesPath, entry.target)
	return "", nil, nil
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
