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

package telemetry

import (
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

var (
	// BazelCommandKey is the bazel subcommand: build, run, test, query, etc.
	BazelCommandKey = attribute.Key("bazel.command")
	// BazelTargetsKey is the set of target patterns passed to the bazel command.
	BazelTargetsKey = attribute.Key("bazel.targets")
	// BazelArgsKey is the full raw argument list passed to the bazel command.
	BazelArgsKey = attribute.Key("bazel.args")
	// BazelInvocationIdKey is the Bazel invocation ID.
	BazelInvocationIdKey = attribute.Key("bazel.invocation_id")
)

// BazelInvocationId returns a span attribute for the given Bazel invocation ID.
func BazelInvocationId(id string) attribute.KeyValue {
	return BazelInvocationIdKey.String(id)
}

// BazelCmdAttrs extracts standard span attributes from a bazel command slice.
// cmd[0] is expected to be the bazel subcommand (e.g. "build", "run", "test").
// Targets are non-flag arguments; anything after a bare "--" is also treated as a target.
func BazelCmdAttrs(cmd []string) []attribute.KeyValue {
	if len(cmd) == 0 {
		return nil
	}
	attrs := []attribute.KeyValue{
		BazelCommandKey.String(cmd[0]),
		BazelArgsKey.StringSlice(cmd[1:]),
	}
	if targets := bazelTargets(cmd); len(targets) > 0 {
		attrs = append(attrs, BazelTargetsKey.StringSlice(targets))
	}
	return attrs
}

// bazelTargets returns the target patterns from a bazel command slice.
// cmd[0] is the subcommand and is skipped. Arguments starting with "-" are treated as flags
// and skipped. A bare "--" ends target parsing; everything after it is passed to the binary
// being run.
func bazelTargets(cmd []string) []string {
	var targets []string
	for _, arg := range cmd[1:] {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			targets = append(targets, arg)
		}
	}
	return targets
}
