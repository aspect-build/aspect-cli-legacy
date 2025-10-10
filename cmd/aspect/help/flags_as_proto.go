package help

import (
	"context"

	"github.com/aspect-build/aspect-cli-legacy/pkg/aspect/root/flags"
	"github.com/aspect-build/aspect-cli-legacy/pkg/bazel"
	"github.com/aspect-build/aspect-cli-legacy/pkg/interceptors"
	"github.com/aspect-build/aspect-cli-legacy/pkg/ioutils"
	"github.com/spf13/cobra"
)

// NewDefaultCmd creates a new flags-as-proto cobra command with the default
// dependencies.
func NewDefaultFlagsAsProtoCmd() *cobra.Command {
	return NewFlagsAsProtoCmd(
		ioutils.DefaultStreams,
		bazel.WorkspaceFromWd,
	)
}

func NewFlagsAsProtoCmd(streams ioutils.Streams, bzl bazel.Bazel) *cobra.Command {
	cmd := cobra.Command{
		Use: "flags-as-proto",
		RunE: interceptors.Run([]interceptors.Interceptor{
			flags.FlagsInterceptor(streams),
		}, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			bazelCmd := []string{"help", "flags-as-proto"}
			return bzl.RunCommand(streams, nil, bazelCmd...)
		}),
	}
	return &cmd
}
