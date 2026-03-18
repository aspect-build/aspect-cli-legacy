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

package run_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	. "github.com/onsi/gomega"

	"github.com/aspect-build/aspect-cli-legacy/pkg/aspect/run"
	"github.com/aspect-build/aspect-cli-legacy/pkg/aspecterrors"
	bazel_mock "github.com/aspect-build/aspect-cli-legacy/pkg/bazel/mock"
	"github.com/aspect-build/aspect-cli-legacy/pkg/ioutils"
	"github.com/aspect-build/aspect-cli-legacy/pkg/plugin/system/bep"
	bep_mock "github.com/aspect-build/aspect-cli-legacy/pkg/plugin/system/bep/mock"
)

// extractInvocationID finds the --invocation_id=<value> arg and returns the value,
// or empty string if not found.
func extractInvocationID(args []string) string {
	for _, arg := range args {
		if after, ok := strings.CutPrefix(arg, "--invocation_id="); ok {
			return after
		}
	}
	return ""
}

func TestRun(t *testing.T) {
	t.Run("when the bazel runner fails, the aspect run fails", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		streams := ioutils.Streams{}
		bzl := bazel_mock.NewMockBazel(ctrl)
		expectErr := &aspecterrors.ExitError{
			Err:      fmt.Errorf("failed to run bazel run"),
			ExitCode: 5,
		}
		bzl.
			EXPECT().
			RunCommand(streams, nil, "run", "//...", "--bes_backend=grpc://127.0.0.1:12345", gomock.Any()).
			Return(expectErr)
		besBackend := bep_mock.NewMockBESBackend(ctrl)
		besBackend.
			EXPECT().
			Args().
			Return([]string{"--bes_backend=grpc://127.0.0.1:12345"}).
			Times(1)
		besBackend.
			EXPECT().
			Errors().
			Times(1)

		ctx := bep.InjectBESInterceptor(context.Background(), besBackend)

		b := run.New(streams, streams, bzl)
		err := b.Run(ctx, nil, []string{"//..."})

		g.Expect(err).To(MatchError(expectErr))
	})

	t.Run("when the BES backend contains errors, the aspect run fails", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		var stderr strings.Builder
		streams := ioutils.Streams{Stderr: &stderr}
		bzl := bazel_mock.NewMockBazel(ctrl)
		bzl.
			EXPECT().
			RunCommand(streams, nil, "run", "//...", "--bes_backend=grpc://127.0.0.1:12345", gomock.Any()).
			Return(nil)
		besBackend := bep_mock.NewMockBESBackend(ctrl)
		besBackend.
			EXPECT().
			Args().
			Return([]string{"--bes_backend=grpc://127.0.0.1:12345"}).
			Times(1)
		besBackend.
			EXPECT().
			Errors().
			Return([]error{
				fmt.Errorf("error 1"),
				fmt.Errorf("error 2"),
			}).
			Times(1)

		ctx := bep.InjectBESInterceptor(context.Background(), besBackend)

		b := run.New(streams, streams, bzl)
		err := b.Run(ctx, nil, []string{"//..."})

		expectedError := fmt.Errorf("2 BES subscriber error(s)")

		g.Expect(err).To(MatchError(expectedError))
		g.Expect(stderr.String()).To(Equal("Error: failed to run 'aspect run' command: error 1\nError: failed to run 'aspect run' command: error 2\n"))
	})

	t.Run("when the bazel runner succeeds, the aspect run succeeds", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		streams := ioutils.Streams{}
		bzl := bazel_mock.NewMockBazel(ctrl)
		bzl.
			EXPECT().
			RunCommand(streams, nil, "run", "//my/runable:target", "--bes_backend=grpc://127.0.0.1:12345", gomock.Any(), "--", "myarg").
			Return(nil)
		besBackend := bep_mock.NewMockBESBackend(ctrl)
		besBackend.
			EXPECT().
			Args().
			Return([]string{"--bes_backend=grpc://127.0.0.1:12345"}).
			Times(1)
		besBackend.
			EXPECT().
			Errors().
			Times(1)

		ctx := bep.InjectBESInterceptor(context.Background(), besBackend)

		b := run.New(streams, streams, bzl)
		err := b.Run(ctx, nil, []string{"//my/runable:target", "--", "myarg"})

		g.Expect(err).To(BeNil())
	})

	t.Run("invocation_id passed to bazel is a valid UUID", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		var capturedArgs []string
		streams := ioutils.Streams{}
		bzl := bazel_mock.NewMockBazel(ctrl)
		bzl.
			EXPECT().
			RunCommand(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ ioutils.Streams, _ *string, args ...string) error {
				capturedArgs = args
				return nil
			})
		besBackend := bep_mock.NewMockBESBackend(ctrl)
		besBackend.EXPECT().Args().Return([]string{}).Times(1)
		besBackend.EXPECT().Errors().Times(1)

		ctx := bep.InjectBESInterceptor(context.Background(), besBackend)

		b := run.New(streams, streams, bzl)
		_ = b.Run(ctx, nil, []string{"//..."})

		invocationID := extractInvocationID(capturedArgs)
		g.Expect(invocationID).NotTo(BeEmpty())
		_, err := uuid.Parse(invocationID)
		g.Expect(err).To(BeNil(), "invocation_id should be a valid UUID, got: %s", invocationID)
	})

	t.Run("each invocation receives a unique invocation_id", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		var capturedIDs []string
		streams := ioutils.Streams{}
		bzl := bazel_mock.NewMockBazel(ctrl)
		bzl.
			EXPECT().
			RunCommand(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ ioutils.Streams, _ *string, args ...string) error {
				capturedIDs = append(capturedIDs, extractInvocationID(args))
				return nil
			}).
			Times(2)
		besBackend := bep_mock.NewMockBESBackend(ctrl)
		besBackend.EXPECT().Args().Return([]string{}).Times(2)
		besBackend.EXPECT().Errors().Times(2)

		ctx := bep.InjectBESInterceptor(context.Background(), besBackend)

		b := run.New(streams, streams, bzl)
		_ = b.Run(ctx, nil, []string{"//..."})
		_ = b.Run(ctx, nil, []string{"//..."})

		g.Expect(capturedIDs).To(HaveLen(2))
		g.Expect(capturedIDs[0]).NotTo(BeEmpty())
		g.Expect(capturedIDs[1]).NotTo(BeEmpty())
		g.Expect(capturedIDs[0]).NotTo(Equal(capturedIDs[1]))
	})

	t.Run("pre-existing invocation_id in args is passed through unchanged", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		fixedID := "a1b2c3d4-0000-0000-0000-000000000000"
		var capturedArgs []string
		streams := ioutils.Streams{}
		bzl := bazel_mock.NewMockBazel(ctrl)
		bzl.
			EXPECT().
			RunCommand(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ ioutils.Streams, _ *string, args ...string) error {
				capturedArgs = args
				return nil
			})
		besBackend := bep_mock.NewMockBESBackend(ctrl)
		besBackend.EXPECT().Args().Return([]string{}).Times(1)
		besBackend.EXPECT().Errors().Times(1)

		ctx := bep.InjectBESInterceptor(context.Background(), besBackend)

		b := run.New(streams, streams, bzl)
		_ = b.Run(ctx, nil, []string{"//...", "--invocation_id=" + fixedID})

		invocationID := extractInvocationID(capturedArgs)
		g.Expect(invocationID).To(Equal(fixedID))

		var invocationIDCount int
		for _, arg := range capturedArgs {
			if strings.HasPrefix(arg, "--invocation_id=") {
				invocationIDCount++
			}
		}
		g.Expect(invocationIDCount).To(Equal(1), "invocation_id should appear exactly once")
	})
}
