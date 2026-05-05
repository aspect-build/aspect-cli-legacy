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

package run

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/aspect-build/aspect-gazelle/runner/pkg/ibp"
)

//go:embed testdata/changedetector_test-compact_exec-a.bin
var execACompressed []byte

func TestExecLogCompact(t *testing.T) {
	r, err := parseCompactExecLogInputs(bytes.NewReader(execACompressed))
	if err != nil {
		t.Errorf("Failed to parse compressed exec log: %v", err)
	}

	slices.Sort(r)
	if len(r) != 2 {
		t.Errorf("Expected 2 inputs, got %d", len(r))
	}
	if !slices.Equal(r, []string{"bazel-out/darwin-fastbuild/bin/apps/project-x/web/dist/src/index.js", "bazel-out/darwin-fastbuild/bin/apps/project-x/web/dist/src/index.js.map"}) {
		t.Errorf("Expected inputs to match")
	}
}

func TestParseRunfilesManifest(t *testing.T) {
	// A small subset of a runfiles manifest copied from a real build
	runfilesManifest := `
_main/README.md /Users/me/dev/repo/README.md
_main/dev_/dev /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/dev_/dev
_main/dev_config.json /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/dev_config.json
_main/dev_node_bin/node /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/dev_node_bin/node
_main/mylib/index.js /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/mylib/index.js
_main/mylib/node_modules/chalk ../../node_modules/.aspect_rules_js/chalk@4.1.2/node_modules/chalk
_main/mylib/package.json /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/mylib/package.json
_main/node_modules/.aspect_rules_js/@bazel+ibazel@0.16.2/node_modules/@bazel/ibazel /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/node_modules/.aspect_rules_js/@bazel+ibazel@0.16.2/node_modules/@bazel/ibazel
_main/node_modules/.aspect_rules_js/@discoveryjs+json-ext@0.5.7/node_modules/@discoveryjs/json-ext /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/node_modules/.aspect_rules_js/@discoveryjs+json-ext@0.5.7/node_modules/@discoveryjs/json-ext
_main/node_modules/.aspect_rules_js/@mycorp+mylib@0.0.0/node_modules/@mycorp/mylib ../../../../../mylib
_main/node_modules/.aspect_rules_js/@mycorp+mypkg@0.0.0/node_modules/@mycorp/mypkg /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/node_modules/.aspect_rules_js/@mycorp+mypkg@0.0.0/node_modules/@mycorp/mypkg
_repo_mapping /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/dev_/dev.repo_mapping
aspect_rules_js~/js/private/js_run_devserver.mjs /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main/bazel-out/darwin_arm64-fastbuild/bin/external/aspect_rules_js~/js/private/js_run_devserver.mjs
aspect_rules_js~/js/private/node-patches/fs.cjs /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/external/aspect_rules_js~/js/private/node-patches/fs.cjs
aspect_rules_js~/js/private/node-patches/register.cjs /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/external/aspect_rules_js~/js/private/node-patches/register.cjs
rules_nodejs~~node~nodejs_darwin_arm64/bin/nodejs/bin/node /private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/external/rules_nodejs~~node~nodejs_darwin_arm64/bin/nodejs/bin/node
`

	r, err := parseRunfilesManifest(strings.NewReader(strings.TrimSpace(runfilesManifest)), "/Users/me/dev/repo", "/private/var/tmp/_bazel_user/3c4e0d2fa783c7ab87494f9a6e5ea2c3/execroot/_main")
	if err != nil {
		t.Errorf("Failed to parse exec log: %v", err)
	}

	if len(r.runfiles) != 16 {
		t.Errorf("Expected 14 runfiles, got %d", len(r.runfiles))
	}

	// 1st-party node_modules
	mylib, mylibExists := r.runfiles["_main/node_modules/.aspect_rules_js/@mycorp+mylib@0.0.0/node_modules/@mycorp/mylib"]
	if !mylibExists {
		t.Errorf("Expected runfile for mylib directory to exist: %v", r.runfiles)
	}
	if mylib.is_source || mylib.is_external || !mylib.is_symlink {
		t.Errorf("Expected mylib to be a symlink, got is_source=%v, is_external=%v, is_symlink=%v", mylib.is_source, mylib.is_external, mylib.is_symlink)
	}

	// Content of that 1st-party package
	if _, mylibContentExists := r.runfiles["_main/mylib/index.js"]; !mylibContentExists {
		t.Errorf("Expected runfile for mylib content to exist: %v", r.runfiles)
	}
	if r.runfilesOriginMapping["bazel-out/darwin_arm64-fastbuild/bin/mylib/index.js"] != "_main/mylib/index.js" {
		t.Errorf("Expected bazel-out/darwin_arm64-fastbuild/bin/mylib/index.js to map to _main/mylib/index.js, got %s", r.runfilesOriginMapping["mylib/index.js"])
	}

	// node_modules of 1st-party packages
	if _, mylibDepExists := r.runfiles["_main/mylib/node_modules/chalk"]; !mylibDepExists {
		t.Errorf("Expected runfile for mylib/node_modules/chalk to exist: %v", r.runfiles)
	}

	// Source files
	if !r.runfiles["_main/README.md"].is_source || r.runfilesOriginMapping["README.md"] != "_main/README.md" {
		t.Errorf("Expected source mappings")
	}

	// External files
	if !r.runfiles["rules_nodejs~~node~nodejs_darwin_arm64/bin/nodejs/bin/node"].is_external {
		t.Errorf("Expected external mappings")
	}
}

// detectChanges(nil) is the path used by runWatch on watchman fresh-instance
// events: cs.Paths is unreliable, so the only reconciliation signal is the
// runfiles manifest. Verify that entries previously in cd.sourcesInfo but
// missing from the latest manifest are recorded as deletions in the cycle
// changes.
func TestDetectChangesNilReconcilesDeletions(t *testing.T) {
	tmp := t.TempDir()
	localExecroot := path.Join(tmp, "_main")
	targetExecutablePath := "myapp_/myapp"
	if err := os.MkdirAll(path.Join(localExecroot, "myapp_"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Runfiles manifest contains kept_file but not deleted_file.
	runfilesManifestPath := path.Join(localExecroot, targetExecutablePath+".runfiles_manifest")
	if err := os.WriteFile(runfilesManifestPath, []byte("_main/kept_file /elsewhere/kept_file\n"), 0644); err != nil {
		t.Fatalf("write runfiles manifest: %v", err)
	}

	// Watch manifest: 5 lines (localExecroot, targetExecutablePath, label, tags, "").
	watchManifestPath := path.Join(tmp, "watch.manifest")
	watchContents := fmt.Sprintf("%s\n%s\n//myapp:myapp\n\n", localExecroot, targetExecutablePath)
	if err := os.WriteFile(watchManifestPath, []byte(watchContents), 0644); err != nil {
		t.Fatalf("write watch manifest: %v", err)
	}
	watchManifest, err := os.Open(watchManifestPath)
	if err != nil {
		t.Fatalf("open watch manifest: %v", err)
	}
	defer watchManifest.Close()

	// A valid (non-empty) zstd-encoded exec log; entries don't affect deletion
	// detection since they're not in our runfiles manifest.
	execLogPath := path.Join(tmp, "execlog.bin")
	if err := os.WriteFile(execLogPath, execACompressed, 0644); err != nil {
		t.Fatalf("write execlog: %v", err)
	}
	execLog, err := os.Open(execLogPath)
	if err != nil {
		t.Fatalf("open execlog: %v", err)
	}
	defer execLog.Close()

	cd := &ChangeDetector{
		workspaceDir:      tmp,
		execlogFile:       execLog,
		watchManifestFile: watchManifest,
		sourcesInfo: ibp.SourceInfoMap{
			"_main/kept_file":    {IsSource: toJsonBoolPtr(true)},
			"_main/deleted_file": {IsSource: toJsonBoolPtr(true)},
		},
		cycleSourceChanges: ibp.SourceInfoMap{},
	}

	if err := cd.detectChanges(nil); err != nil {
		t.Fatalf("detectChanges(nil): %v", err)
	}

	if _, ok := cd.sourcesInfo["_main/kept_file"]; !ok {
		t.Errorf("kept_file should remain in sourcesInfo, got %v", cd.sourcesInfo)
	}
	if _, ok := cd.sourcesInfo["_main/deleted_file"]; ok {
		t.Errorf("deleted_file should be removed from sourcesInfo")
	}

	changes := cd.cycleChanges()
	si, ok := changes["_main/deleted_file"]
	if !ok {
		t.Errorf("expected deletion marker for deleted_file in cycleChanges, got %v", changes)
	} else if si != nil {
		t.Errorf("expected nil SourceInfo (deletion marker), got %+v", si)
	}
}
