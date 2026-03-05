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

package configure

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/aspect-build/aspect-cli-legacy/pkg/bazel"
	"github.com/aspect-build/aspect-cli-legacy/pkg/ioutils"
	BazelLog "github.com/aspect-build/aspect-gazelle/common/logger"
)

// The @gazelle go_deps extension name.
// https://github.com/bazel-contrib/bazel-gazelle/blob/v0.39.1/internal/bzlmod/go_deps.bzl#L827
const GO_DEPS_EXTENSION_NAME = "go_deps"

// The repository name for the gazelle repo_config.
// https://github.com/bazel-contrib/bazel-gazelle/blob/v0.39.1/internal/bzlmod/go_deps.bzl#L648-L654
const GO_REPOSITORY_CONFIG_REPO_NAME = "bazel_gazelle_go_repository_config"

// The standard repository name for the go_sdk from rules_go.
const GO_SDK_REPO_NAME = "go_sdk"

// bazel 8 switches the bzlmod separator to "+"
// See https://github.com/bazelbuild/bazel/issues/23127
var BZLMOD_REPO_SEPARATORS = []string{"~", "+"}

// setupGoRoot discovers GOROOT from the workspace's Bazel-configured @go_sdk
// and sets the GOROOT environment variable so the Go language plugin uses the
// correct go binary rather than whatever is on PATH.
//
// Skipped if GOROOT is already set.
func setupGoRoot() {
	if os.Getenv("GOROOT") != "" {
		return
	}

	bzl := bazel.WorkspaceFromWd
	var out strings.Builder
	streams := ioutils.Streams{Stdout: &out, Stderr: nil}
	if err := bzl.RunCommand(streams, nil, "run", "--ui_event_filters=-info,-debug", "--noshow_progress", fmt.Sprintf("@%s//:bin/go", GO_SDK_REPO_NAME), "--", "env", "GOROOT"); err != nil {
		BazelLog.Infof("Could not determine GOROOT from Bazel @%s: %v", GO_SDK_REPO_NAME, err)
		return
	}

	goroot := strings.TrimSpace(out.String())
	if goroot == "" {
		return
	}

	BazelLog.Infof("Setting GOROOT=%s (from Bazel @go_sdk)", goroot)
	os.Setenv("GOROOT", goroot)
}

func determineGoRepositoryConfigPath() (string, error) {
	// TODO(jason): look into a store of previous invocations for relevant logs
	bzl := bazel.WorkspaceFromWd

	var out strings.Builder
	streams := ioutils.Streams{Stdout: &out, Stderr: nil}
	if err := bzl.RunCommand(streams, nil, "info", "output_base"); err != nil {
		return "", fmt.Errorf("unable to locate output_base: %w", err)
	}

	outputBase := strings.TrimSpace(out.String())
	if outputBase == "" {
		return "", fmt.Errorf("unable to locate output_base on path")
	}

	var goDepsRepoName string
	for _, sep := range BZLMOD_REPO_SEPARATORS {
		repoName := fmt.Sprintf("gazelle%s%s%s%s%s/WORKSPACE", sep, sep, GO_DEPS_EXTENSION_NAME, sep, GO_REPOSITORY_CONFIG_REPO_NAME)
		repoPath := path.Join(outputBase, "external", repoName)

		_, err := os.Stat(repoPath)
		if err == nil {
			goDepsRepoName = repoPath
			break
		}
	}

	if goDepsRepoName == "" {
		// Assume no matches means rules_go is not being used in bzlmod
		// or the gazelle `go_deps` extension is not being used
		BazelLog.Infof("No %s found in output_base: %s", GO_REPOSITORY_CONFIG_REPO_NAME, outputBase)
		return "", nil
	}

	BazelLog.Infof("Found %s(s): %v", GO_REPOSITORY_CONFIG_REPO_NAME, goDepsRepoName)

	return goDepsRepoName, nil
}
