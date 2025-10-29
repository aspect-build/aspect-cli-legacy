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

package bep

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"syscall"
	"time"

	buildeventstream "github.com/aspect-build/aspect-cli-legacy/bazel/buildeventstream"
	"github.com/aspect-build/aspect-cli-legacy/pkg/aspecterrors"
	"github.com/aspect-build/aspect-cli-legacy/pkg/plugin/system/besproxy"
	"golang.org/x/sync/errgroup"
	buildv1 "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protodelim"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type BESPipeInterceptor interface {
	BESInterceptor
	Setup() error
}

const besEventGlobalTimeoutDuration = 5 * time.Minute
const besEventThrottleDuration = 50 * time.Millisecond

func NewBESPipe(buildId, invocationId string) (BESPipeInterceptor, error) {
	return &besPipe{
		bepBinPath:  path.Join(os.TempDir(), fmt.Sprintf("aspect-cli-%v-bes.bin", os.Getpid())),
		errors:      &aspecterrors.ErrorList{},
		subscribers: &subscriberList{},

		besBuildId:      buildId,
		besInvocationId: invocationId,
	}, nil
}

type besPipe struct {
	bepBinPath  string
	errors      *aspecterrors.ErrorList
	errorsMutex sync.RWMutex
	subscribers *subscriberList

	besBuildId      string
	besInvocationId string
	besProxies      []besproxy.BESProxy
}

var _ BESPipeInterceptor = (*besPipe)(nil)

func (bb *besPipe) Setup() error {
	err := syscall.Mknod(bb.bepBinPath, syscall.S_IFIFO|0666, 0)
	if err != nil {
		return fmt.Errorf("failed to create BES pipe %s: %w", bb.bepBinPath, err)
	}
	return nil
}

func (bb *besPipe) RegisterBesProxy(ctx context.Context, p besproxy.BESProxy) {
	bb.besProxies = append(bb.besProxies, p)

	bb.sendInitialLifecycleEvents(ctx, p)

	err := p.PublishBuildToolEventStream(ctx, grpc.WaitForReady(false))
	if err != nil {
		// If we fail to create the build event stream to a proxy then print out an error but don't fail the GRPC call
		fmt.Fprintf(os.Stderr, "Error creating build event stream to %v: %s\n", p.Host(), err.Error())
	}

	// Run a goroutine to recv ACKs from the grpc stream
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// If the proxy is not healthy, break out of the loop
			if !p.Healthy() {
				break
			}
			_, err := p.Recv()
			if err != nil {
				if err != io.EOF {
					if status.Code(err) == codes.Canceled {
						break
					}
					// If we fail to recv an ack from a proxy then print out an error but don't fail the GRPC call
					fmt.Fprintf(os.Stderr, "error receiving build event stream ack %v: %s\n", p.Host(), err.Error())
				}
				break
			}
		}
	}()
}

func (bb *besPipe) sendInitialLifecycleEvents(ctx context.Context, p besproxy.BESProxy) {
	// https://github.com/bazelbuild/bazel/blob/198c4c8aae1b5ef3d202f602932a99ce19707fc4/src/main/java/com/google/devtools/build/lib/buildeventservice/client/BuildEventServiceProtoUtil.java#L73
	p.PublishLifecycleEvent(ctx, lifecycleRequest(bb.besBuildId, bb.besInvocationId, 1, &buildv1.BuildEvent{
		Event: &buildv1.BuildEvent_BuildEnqueued_{},
	}))

	// https://github.com/bazelbuild/bazel/blob/198c4c8aae1b5ef3d202f602932a99ce19707fc4/src/main/java/com/google/devtools/build/lib/buildeventservice/client/BuildEventServiceProtoUtil.java#L95
	p.PublishLifecycleEvent(ctx, lifecycleRequest(bb.besBuildId, bb.besInvocationId, 2, &buildv1.BuildEvent{
		Event: &buildv1.BuildEvent_InvocationAttemptStarted_{},
	}))
}

func (bb *besPipe) sendFinalLifecycleEvents(ctx context.Context, p besproxy.BESProxy) {
	// https://github.com/bazelbuild/bazel/blob/198c4c8aae1b5ef3d202f602932a99ce19707fc4/src/main/java/com/google/devtools/build/lib/buildeventservice/client/BuildEventServiceProtoUtil.java#L84
	p.PublishLifecycleEvent(ctx, lifecycleRequest(bb.besBuildId, bb.besInvocationId, 2, &buildv1.BuildEvent{
		Event: &buildv1.BuildEvent_InvocationAttemptFinished_{},
	}))

	// https://github.com/bazelbuild/bazel/blob/198c4c8aae1b5ef3d202f602932a99ce19707fc4/src/main/java/com/google/devtools/build/lib/buildeventservice/client/BuildEventServiceProtoUtil.java#L108
	p.PublishLifecycleEvent(ctx, lifecycleRequest(bb.besBuildId, bb.besInvocationId, 2, &buildv1.BuildEvent{
		Event: &buildv1.BuildEvent_BuildFinished_{
			BuildFinished: &buildv1.BuildEvent_BuildFinished{
				// TODO: need status
			},
		},
	}))
}

func lifecycleRequest(buildId, invocationId string, sequenceNumber int64, event *buildv1.BuildEvent) *buildv1.PublishLifecycleEventRequest {
	return &buildv1.PublishLifecycleEventRequest{
		ServiceLevel: buildv1.PublishLifecycleEventRequest_INTERACTIVE,
		BuildEvent: &buildv1.OrderedBuildEvent{
			SequenceNumber: sequenceNumber,
			StreamId: &buildv1.StreamId{
				BuildId:      buildId,
				InvocationId: invocationId,
			},
			Event: event,
		},
	}
}

func (bb *besPipe) ServeWait(ctx context.Context) error {
	go func() {
		conn, err := os.OpenFile(bb.bepBinPath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			bb.errorsMutex.Lock()
			defer bb.errorsMutex.Unlock()
			bb.errors.Insert(fmt.Errorf("failed to accept connection on BES pipe %s: %w", bb.bepBinPath, err))
			return
		}

		defer func() {
			conn.Close()
		}()

		if err := bb.streamBesEvents(ctx, conn); err != nil {
			bb.errorsMutex.Lock()
			defer bb.errorsMutex.Unlock()
			bb.errors.Insert(fmt.Errorf("failed to stream BES events: %w", err))
			return
		}

		for _, p := range bb.besProxies {
			if !p.Healthy() {
				continue
			}

			bb.sendFinalLifecycleEvents(context.Background(), p)

			if err := p.CloseSend(); err != nil {
				fmt.Fprintf(os.Stderr, "Error closing build event stream to %v: %s\n", p.Host(), err.Error())
			}
		}
	}()
	return nil
}

func (bb *besPipe) streamBesEvents(ctx context.Context, r io.Reader) error {
	reader := bufio.NewReader(r)

	// Manually manage a sequence ID for the events
	seqId := int64(0)

	besEventGlobalTimeout := time.After(besEventGlobalTimeoutDuration)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		event := buildeventstream.BuildEvent{}

		if err := protodelim.UnmarshalFrom(reader, &event); err != nil {
			if errors.Is(err, io.EOF) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-besEventGlobalTimeout:
					return fmt.Errorf("timeout reached while waiting for BES events")
				case <-time.After(besEventThrottleDuration):
					// throttle the reading of the BES file when no new data is available
					continue
				}
			}
			return fmt.Errorf("failed to parse BES event: %w", err)
		}

		// Reset the global timeout on each received event
		besEventGlobalTimeout = time.After(besEventGlobalTimeoutDuration)

		seqId++

		if err := bb.publishBesEvent(seqId, &event); err != nil {
			return fmt.Errorf("failed to publish BES event: %w", err)
		}

		if event.LastMessage {
			break
		}
	}

	return nil
}

func (bb *besPipe) publishBesEvent(seqId int64, event *buildeventstream.BuildEvent) error {
	eg := errgroup.Group{}

	for s := bb.subscribers.head; s != nil; s = s.next {
		cb := s.callback
		eg.Go(
			func() error {
				return cb(event, seqId)
			},
		)
	}

	if len(bb.besProxies) > 0 {
		marshaledEvent, err := anypb.New(event)
		if err != nil {
			return fmt.Errorf("failed to marshal BES event: %w", err)
		}

		// Wrap the event in the gRPC message
		grpcEvent := &buildv1.PublishBuildToolEventStreamRequest{
			OrderedBuildEvent: &buildv1.OrderedBuildEvent{
				SequenceNumber: seqId,
				StreamId: &buildv1.StreamId{
					BuildId:      bb.besBuildId,
					InvocationId: bb.besInvocationId,
				},
				Event: &buildv1.BuildEvent{
					EventTime: timestamppb.Now(),
					Event:     &buildv1.BuildEvent_BazelEvent{BazelEvent: marshaledEvent},
				},
			},
		}

		for _, p := range bb.besProxies {
			eg.Go(
				func() error {
					if err := p.Send(grpcEvent); err != nil {
						fmt.Fprintf(os.Stderr, "Error sending BES event to %v: %s\n", p.Host(), err.Error())
					}
					return nil
				},
			)
		}
	}

	return eg.Wait()
}

func (bb *besPipe) Args() []string {
	args := []string{
		"--build_event_publish_all_actions",
		"--build_event_binary_file",
		bb.bepBinPath,
	}

	// Also add wait_for_upload_complete flag if the bes pipe was explicitly requested.
	// NOTE: this is explicitly not the default behavior to avoid breaking changes in bazel6
	if os.Getenv("ASPECT_BEP_USE_PIPE") != "" {
		args = append(args, "--build_event_binary_file_upload_mode=wait_for_upload_complete")
	}

	return args
}

func (bb *besPipe) RegisterSubscriber(callback CallbackFn, multiThreaded bool) {
	bb.subscribers.Insert(callback)
}

func (bb *besPipe) Errors() []error {
	bb.errorsMutex.RLock()
	defer bb.errorsMutex.RUnlock()
	return bb.errors.Errors()
}

func (bb *besPipe) GracefulStop() {
	os.Remove(bb.bepBinPath)
}
