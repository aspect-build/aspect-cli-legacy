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

package flags_test

import (
	"testing"

	"github.com/aspect-build/aspect-cli-legacy/pkg/aspect/root/flags"
	. "github.com/onsi/gomega"
)

func TestFindInvocationId(t *testing.T) {
	t.Run("equals form", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "--invocation_id=abc"})).To(Equal("abc"))
	})

	t.Run("space-separated form", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "--invocation_id", "abc"})).To(Equal("abc"))
	})

	t.Run("not present returns empty string", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "//..."})).To(Equal(""))
	})

	t.Run("last occurrence wins", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "--invocation_id=first", "--invocation_id=last"})).To(Equal("last"))
	})

	t.Run("stops at bare --", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "//app", "--", "--invocation_id=binary-arg"})).To(Equal(""))
	})

	t.Run("flag before -- is found, flag after -- is ignored", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "--invocation_id=real", "--", "--invocation_id=binary-arg"})).To(Equal("real"))
	})

	t.Run("--invocation_id at end of args with no value is ignored", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "--invocation_id"})).To(Equal(""))
	})

	t.Run("--invocation_id immediately before -- is ignored", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(flags.FindInvocationId([]string{"run", "--invocation_id", "--"})).To(Equal(""))
	})
}
