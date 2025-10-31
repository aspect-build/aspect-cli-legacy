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

package system

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"gopkg.in/yaml.v3"

	"github.com/aspect-build/aspect-cli-legacy/pkg/aspect/root/config"
	rootFlags "github.com/aspect-build/aspect-cli-legacy/pkg/aspect/root/flags"
	"github.com/aspect-build/aspect-cli-legacy/pkg/aspecterrors"
	"github.com/aspect-build/aspect-cli-legacy/pkg/interceptors"
	"github.com/aspect-build/aspect-cli-legacy/pkg/ioutils"
	"github.com/aspect-build/aspect-cli-legacy/pkg/ioutils/prompt"
	"github.com/aspect-build/aspect-cli-legacy/pkg/plugin/client"
	"github.com/aspect-build/aspect-cli-legacy/pkg/plugin/sdk/v1alpha4/plugin"
	"github.com/aspect-build/aspect-cli-legacy/pkg/plugin/system/bep"
	"github.com/aspect-build/aspect-cli-legacy/pkg/plugin/system/besproxy"
)

// PluginSystem is the interface that defines all the methods for the aspect CLI
// plugin system intended to be used by the Core.
type PluginSystem interface {
	Configure(streams ioutils.Streams, pluginsConfig interface{}) error
	TearDown()
	RegisterCustomCommands(cmd *cobra.Command, bazelStartupArgs []string) error
	// Create an Interceptor for plugins if necessary.
	// The interceptor may use a BES backend or binary-file to receive build event stream depending
	// on system configuration.
	BESPluginInterceptor() interceptors.Interceptor
	// An Interceptor always created and always using a binary-file.
	BESPipeInterceptor() interceptors.Interceptor
	BuildHooksInterceptor(streams ioutils.Streams) interceptors.Interceptor
	TestHooksInterceptor(streams ioutils.Streams) interceptors.Interceptor
	RunHooksInterceptor(streams ioutils.Streams) interceptors.Interceptor
}

type pluginSystem struct {
	clientFactory client.Factory
	plugins       *PluginList
	promptRunner  prompt.PromptRunner
}

// NewPluginSystem instantiates a default internal implementation of the
// PluginSystem interface.
func NewPluginSystem() PluginSystem {
	return &pluginSystem{
		clientFactory: client.NewFactory(),
		plugins:       &PluginList{},
		promptRunner:  prompt.NewPromptRunner(),
	}
}

// Configure configures the plugin system.
func (ps *pluginSystem) Configure(streams ioutils.Streams, pluginsConfig interface{}) error {
	plugins, err := config.UnmarshalPluginConfig(pluginsConfig)
	if err != nil {
		return fmt.Errorf("failed to configure plugin system: %w", err)
	}

	g := new(errgroup.Group)
	var mutex sync.Mutex

	for _, p := range plugins {
		p := p

		g.Go(func() error {
			aspectplugin, err := ps.clientFactory.New(p, streams)
			if err != nil {
				return err
			}
			if aspectplugin == nil {
				return nil
			}

			properties, err := yaml.Marshal(p.Properties)
			if err != nil {
				return err
			}

			setupConfig := plugin.NewSetupConfig(properties)
			if err := aspectplugin.Setup(setupConfig); err != nil {
				return err
			}

			mutex.Lock()
			ps.plugins.insert(aspectplugin)
			mutex.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to configure plugin system: %w", err)
	}

	return nil
}

// RegisterCustomCommands processes custom commands provided by plugins and adds
// them as commands to the core whilst setting up callbacks for the those commands.
func (ps *pluginSystem) RegisterCustomCommands(cmd *cobra.Command, bazelStartupArgs []string) error {
	internalCommands := make(map[string]struct{})
	for _, command := range cmd.Commands() {
		cmdName := strings.SplitN(command.Use, " ", 2)[0]
		internalCommands[cmdName] = struct{}{}
	}

	for node := ps.plugins.head; node != nil; node = node.next {
		result, err := node.payload.Plugin.CustomCommands()
		if err != nil {
			return fmt.Errorf("failed to register custom commands: %w", err)
		}

		for _, command := range result {
			cmdName := strings.SplitN(command.Use, " ", 2)[0]
			if _, ok := internalCommands[cmdName]; ok {
				return fmt.Errorf("failed to register custom commands: plugin implements a command with a protected name: %s", command.Use)
			}

			callback := node.payload.CustomCommandExecutor

			cmd.AddCommand(&cobra.Command{
				Use:     command.Use,
				Short:   command.ShortDesc,
				Long:    command.LongDesc,
				GroupID: "plugin",
				RunE: interceptors.Run(
					[]interceptors.Interceptor{},
					func(ctx context.Context, cmd *cobra.Command, args []string) (exitErr error) {
						return callback.ExecuteCustomCommand(cmdName, ctx, args, bazelStartupArgs)
					},
				),
			})
		}
	}
	return nil
}

// TearDown tears down the plugin system, making all the necessary actions to
// clean up the system.
func (ps *pluginSystem) TearDown() {
	for node := ps.plugins.head; node != nil; node = node.next {
		node.payload.Kill()
	}
}

// BESPipeInterceptor always starts a BES backend and injects it into the context.
// Use BESInterceptor to only create the grpc service when there is a known subscriber.
func (ps *pluginSystem) BESPipeInterceptor() interceptors.Interceptor {
	return func(ctx context.Context, cmd *cobra.Command, args []string, next interceptors.RunEContextFn) error {
		return ps.createBesInterceptor(ctx, cmd, args, true, next)
	}
}

// BESPluginInterceptor sometimes starts a BES backend or binary-file and injects it into the context.
// It short-circuits and does nothing in cases where we think there is no subscriber.
// It gracefully stops the server after the main command is executed.
func (ps *pluginSystem) BESPluginInterceptor() interceptors.Interceptor {
	return func(ctx context.Context, cmd *cobra.Command, args []string, next interceptors.RunEContextFn) error {
		// Check if --aspect:force_bes_backend is set. This is primarily used for testing.
		forceBesBackend, err := cmd.Root().Flags().GetBool(rootFlags.AspectForceBesBackendFlagName)
		if err != nil {
			return fmt.Errorf("failed to get value of --aspect:force_bes_backend: %w", err)
		}

		// If there are no plugins configured and --aspect:force_bes_backend is not set then short
		// circuit here since we don't have any need to create a grpc server to consume the build event
		// stream.
		if !(forceBesBackend || ps.hasBESPlugins()) {
			return next(ctx, cmd, args)
		}
		if forceBesBackend {
			fmt.Fprintf(os.Stderr, "Forcing creation of BES backend\n")
		}

		usePipe := os.Getenv("ASPECT_BEP_USE_PIPE") != ""

		return ps.createBesInterceptor(ctx, cmd, args, usePipe, next)
	}
}

// Check if any plugins are registered that require BES event processing
func (ps *pluginSystem) hasBESPlugins() bool {
	for node := ps.plugins.head; node != nil; node = node.next {
		if !node.payload.DisableBESEvents {
			return true
		}
	}
	return false
}

func determineBuildId(args []string) string {
	return uuid.NewString()
}

func determineInvocationId(args []string) string {
	invocationId := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "--invocation_id=") {
			invocationId = strings.TrimPrefix(arg, "--invocation_id=")
		}
	}
	// Default to random UUID if not provided on the CLI
	if invocationId == "" {
		invocationId = uuid.NewString()
	}
	return invocationId
}

func removeLastBesBackend(args []string) ([]string, string) {
	// Find the last --bes_backend
	lastBackend := -1
	for idx, arg := range args {
		if strings.HasPrefix(arg, "--bes_backend=") {
			lastBackend = idx
		}
	}

	// The "last --bes_backend" is expected to be the aspect rosetta grpc backend
	if lastBackend == -1 {
		panic("No --bes_backend found to pipe last BES events to")
	}

	backend := strings.TrimPrefix(args[lastBackend], "--bes_backend=")
	if !strings.HasPrefix(backend, "grpc://") {
		panic("Only grpc:// BES backends are supported for piping last BES events, received: " + backend)
	}

	// Remove + return the last bes_backend
	return slices.Delete(args, lastBackend, lastBackend+1), backend
}

func (ps *pluginSystem) createBesInterceptor(ctx context.Context, cmd *cobra.Command, args []string, usePipe bool, next interceptors.RunEContextFn) error {
	var besInterceptor bep.BESInterceptor
	var err error

	if usePipe {
		besInterceptor, err = setupBesPipe(args)
		if err != nil {
			return err
		}
	} else {
		besInterceptor, err = setupBesBackend()
		if err != nil {
			return err
		}
	}

	// Start the BES backend
	if err := besInterceptor.ServeWait(ctx); err != nil {
		return fmt.Errorf("failed to run BES backend: %w", err)
	}
	defer besInterceptor.GracefulStop()

	for node := ps.plugins.head; node != nil; node = node.next {
		if !node.payload.DisableBESEvents {
			besInterceptor.RegisterSubscriber(node.payload.BEPEventCallback, node.payload.MultiThreaded)
		}
	}

	if os.Getenv("ASPECT_BEP_WRITE_LAST_VIA_PIPE") != "" {
		newArgs, lastBackend := removeLastBesBackend(args)

		besProxy := besproxy.NewBesProxy(lastBackend, map[string]string{})
		if err := besProxy.Connect(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to connect to build event stream backend %s: %s", lastBackend, err.Error())
		} else {
			besInterceptor.RegisterBesProxy(ctx, besProxy)
		}

		args = newArgs
	}

	ctx = bep.InjectBESInterceptor(ctx, besInterceptor)
	return next(ctx, cmd, args)
}

func setupBesPipe(args []string) (bep.BESPipeInterceptor, error) {
	buildId := determineBuildId(args)
	invocationId := determineInvocationId(args)
	besPipe, err := bep.NewBESPipe(buildId, invocationId)
	if err != nil {
		return nil, fmt.Errorf("failed to create BES pipe: %w", err)
	}
	if err := besPipe.Setup(); err != nil {
		return nil, fmt.Errorf("failed to setup BES pipe: %w", err)
	}
	return besPipe, nil
}

func setupBesBackend() (bep.BESInterceptor, error) {
	besBackend := bep.NewBESBackend()
	opts := []grpc.ServerOption{
		// Bazel doesn't seem to set a maximum send message size, therefore
		// we match the default send message for Go, which should be enough
		// for all messages sent by Bazel (roughly 2.14GB).
		grpc.MaxRecvMsgSize(math.MaxInt32),
		// Here we are just being explicit with the default value since we
		// also set the receive message size.
		grpc.MaxSendMsgSize(math.MaxInt32),
		// Allow pings as frequent as every 1s
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             1 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	// Setup the BES backend grpc server
	if err := besBackend.Setup(opts...); err != nil {
		return nil, fmt.Errorf("failed to run BES backend: %w", err)
	}

	return besBackend, nil
}

// BuildHooksInterceptor returns an interceptor that runs the pre and post-build
// hooks from all plugins.
func (ps *pluginSystem) BuildHooksInterceptor(streams ioutils.Streams) interceptors.Interceptor {
	return ps.commandHooksInterceptor("PostBuildHook", streams)
}

// TestHooksInterceptor returns an interceptor that runs the pre and post-test
// hooks from all plugins.
func (ps *pluginSystem) TestHooksInterceptor(streams ioutils.Streams) interceptors.Interceptor {
	return ps.commandHooksInterceptor("PostTestHook", streams)
}

// RunHooksInterceptor returns an interceptor that runs the pre and post-run
// hooks from all plugins.
func (ps *pluginSystem) RunHooksInterceptor(streams ioutils.Streams) interceptors.Interceptor {
	return ps.commandHooksInterceptor("PostRunHook", streams)
}

func (ps *pluginSystem) commandHooksInterceptor(methodName string, streams ioutils.Streams) interceptors.Interceptor {
	return func(ctx context.Context, cmd *cobra.Command, args []string, next interceptors.RunEContextFn) (exitErr error) {
		isInteractiveMode, err := cmd.Root().PersistentFlags().GetBool(rootFlags.AspectInteractiveFlagName)
		if err != nil {
			return fmt.Errorf("failed to run 'aspect %s' command: %w", cmd.CalledAs(), err)
		}

		defer func() {
			hasPluginErrors := false
			for node := ps.plugins.head; node != nil; node = node.next {
				params := []reflect.Value{
					reflect.ValueOf(isInteractiveMode),
					reflect.ValueOf(ps.promptRunner),
				}
				if err := reflect.ValueOf(node.payload).MethodByName(methodName).Call(params)[0].Interface(); err != nil {
					fmt.Fprintf(streams.Stderr, "Error: failed to run 'aspect %s' command: %v\n", cmd.CalledAs(), err)
					hasPluginErrors = true
				}
			}
			if hasPluginErrors {
				var err *aspecterrors.ExitError
				if errors.As(exitErr, &err) {
					err.ExitCode = 1
				}
			}
		}()
		return next(ctx, cmd, args)
	}
}

// PluginList implements a simple linked list for the parsed plugins from the
// plugins file.
type PluginList struct {
	head *PluginNode
	tail *PluginNode
}

func (l *PluginList) insert(p *client.PluginInstance) {
	node := &PluginNode{payload: p}
	if l.head == nil {
		l.head = node
	} else {
		l.tail.next = node
	}
	l.tail = node
}

// PluginNode is a node in the PluginList linked list.
type PluginNode struct {
	next    *PluginNode
	payload *client.PluginInstance
}
