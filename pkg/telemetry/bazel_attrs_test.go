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
	"testing"
)

func TestBazelTargets(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "single target",
			args: []string{"build", "//foo:bar"},
			want: []string{"//foo:bar"},
		},
		{
			name: "multiple targets",
			args: []string{"build", "//foo:bar", "//baz:qux"},
			want: []string{"//foo:bar", "//baz:qux"},
		},
		{
			name: "flags are skipped",
			args: []string{"build", "--flag", "//foo:bar", "--other=val"},
			want: []string{"//foo:bar"},
		},
		{
			name: "args after -- are excluded",
			args: []string{"build", "//foo:bar", "--", "ignore", "--these", "//and:these"},
			want: []string{"//foo:bar"},
		},
		{
			name: "args after run //target are excluded",
			args: []string{"run", "//foo:bar", "ignore", "--these", "//and:these"},
			want: []string{"//foo:bar"},
		},
		{
			name: "run with space-separated flag value before target",
			args: []string{"run", "--invocation_id", "abc-123", "//pkg:bin"},
			want: []string{"//pkg:bin"},
		},
		{
			name: "run with boolean flag before //target",
			args: []string{"run", "--keep_going", "//foo:bin"},
			want: []string{"//foo:bin"},
		},
		{
			name: "run with boolean flag before bare target",
			args: []string{"run", "--keep_going", "target"},
			want: []string{"target"},
		},
		{
			name: "run with multiple boolean flags before bare target",
			args: []string{"run", "--keep_going", "--verbose_failures", "target"},
			want: []string{"target"},
		},
		{
			name: "run with space-separated flag value before bare target",
			args: []string{"run", "--invocation_id", "abc-123", "target"},
			want: []string{"target"},
		},
		{
			name: "run with value flag then boolean flag before bare target",
			args: []string{"run", "--invocation_id", "abc-123", "--keep_going", "target"},
			want: []string{"target"},
		},
		{
			name: "only -- separator",
			args: []string{"run", "--", "//foo:bar"},
			want: nil,
		},
		{
			name: "command only",
			args: []string{"build"},
			want: nil,
		},
		{
			name: "build with only space-separated flag value",
			args: []string{"build", "--flag", "value"},
			want: nil,
		},
		{
			name: "wildcard target",
			args: []string{"build", "//..."},
			want: []string{"//..."},
		},
		{
			name: "external target",
			args: []string{"build", "@repo//pkg:target"},
			want: []string{"@repo//pkg:target"},
		},
		{
			name: "test command with flags",
			args: []string{"test", "--test_output=errors", "//foo:bar_test"},
			want: []string{"//foo:bar_test"},
		},
		{
			name: "run command with binary args after --",
			args: []string{"run", "//foo:bin", "--", "--binary-flag", "arg"},
			want: []string{"//foo:bin"},
		},
		{
			name: "build command with multiple targets and flags",
			args: []string{"build", "--config=opt", "//foo:bar", "//baz/...", ":boo"},
			want: []string{"//foo:bar", "//baz/...", ":boo"},
		},
		{
			name: "relative label with colon",
			args: []string{"build", ":target"},
			want: []string{":target"},
		},
		{
			name: "relative label without colon",
			args: []string{"run", "target"},
			want: []string{"target"},
		},
		{
			name: "absolute label without colon",
			args: []string{"build", "//foo/bar"},
			want: []string{"//foo/bar"},
		},
		{
			name: "external label without colon",
			args: []string{"build", "@repo//pkg"},
			want: []string{"@repo//pkg"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bazelTargets(tc.args)
			if len(got) != len(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
					return
				}
			}
		})
	}
}
