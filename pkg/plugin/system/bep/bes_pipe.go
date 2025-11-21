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
const gracePeriodDuration = 2 * time.Second
const besSendTimeout = 1 * time.Minute

func NewBESPipe(buildId, invocationId string) (BESPipeInterceptor, error) {
	return &besPipe{
		bepBinPath:  path.Join(os.TempDir(), fmt.Sprintf("aspect-cli-%v-bes.bin", os.Getpid())),
		errors:      &aspecterrors.ErrorList{},
		subscribers: &subscriberList{},

		besBuildId:      buildId,
		besInvocationId: invocationId,
		wg:              &sync.WaitGroup{},
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

	// Track whether we have already unlinked the pipe due to backend failure
	pipeAborted sync.Once

	wg *sync.WaitGroup
}

var _ BESPipeInterceptor = (*besPipe)(nil)

func (bb *besPipe) Setup() error {
	err := syscall.Mknod(bb.bepBinPath, syscall.S_IFIFO|0o666, 0)
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
		return
	}

	// Run a goroutine to recv ACKs from the grpc stream
	go func() {
		for {
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
		// When the ACK goroutine exits (usually because of error), check if we should abort the pipe
		bb.maybeAbortPipeBecauseNoHealthyBackends()
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
	bb.wg.Add(1)
	go func() {
		defer bb.wg.Done()

		// If the overall context is cancelled, abort the pipe immediately
		go func() {
			<-ctx.Done()
			bb.maybeAbortPipeBecauseNoHealthyBackends()
		}()

		conn, err := os.OpenFile(bb.bepBinPath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			bb.errorsMutex.Lock()
			defer bb.errorsMutex.Unlock()
			bb.errors.Insert(fmt.Errorf("failed to accept connection on BES pipe %s: %w", bb.bepBinPath, err))
			return
		}
		defer conn.Close()

		if err := bb.streamBesEvents(ctx, conn); err != nil {
			bb.errorsMutex.Lock()
			defer bb.errorsMutex.Unlock()
			bb.errors.Insert(fmt.Errorf("failed to stream BES events: %w", err))
			return
		}

		// Normal completion path
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

// maybeAbortPipeBecauseNoHealthyBackends unlinks the FIFO if all backends are unhealthy.
// This gives Bazel a broken pipe → it aborts the upload and exits.
func (bb *besPipe) maybeAbortPipeBecauseNoHealthyBackends() {
	if len(bb.besProxies) == 0 {
		return
	}

	var anyHealthy bool
	for _, p := range bb.besProxies {
		if p.Healthy() {
			anyHealthy = true
			break
		}
	}
	if anyHealthy {
		return
	}

	bb.pipeAborted.Do(func() {
		fmt.Fprintf(os.Stderr, "All BES backends are unhealthy — unlinking pipe %s\n", bb.bepBinPath)
		if err := syscall.Unlink(bb.bepBinPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "failed to unlink BES pipe %s: %v\n", bb.bepBinPath, err)
		}
	})
}

func (bb *besPipe) streamBesEvents(ctx context.Context, conn *os.File) error {
	reader := bufio.NewReader(conn)

	go func() {
		<-ctx.Done()
		if err := conn.SetReadDeadline(time.Now().Add(gracePeriodDuration)); err != nil {
			fmt.Fprintf(os.Stderr, "failed to set read deadline after context done: %s\n", err.Error())
		}
	}()

	// Manually manage a sequence ID for the events
	seqId := int64(0)

	besEventGlobalTimeout := time.After(besEventGlobalTimeoutDuration)
	for {
		event := buildeventstream.BuildEvent{}

		opts := protodelim.UnmarshalOptions{
			MaxSize: 32 * 1024 * 1024, // 32 MB max; we have observed 17 MB BES events in the wild
		}

		if err := opts.UnmarshalFrom(reader, &event); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return fmt.Errorf("timeout reached while waiting for BES events")
			}
			if errors.Is(err, io.EOF) {
				select {
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

	// Clear read deadline
	conn.SetReadDeadline(time.Time{})

	return nil
}

func (bb *besPipe) publishBesEvent(seqId int64, event *buildeventstream.BuildEvent) error {
	eg := errgroup.Group{}

	for s := bb.subscribers.head; s != nil; s = s.next {
		cb := s.callback
		eg.Go(
			func() error {
				var invocationId string
				if os.Getenv("ASPECT_BEP_WRITE_LAST_VIA_PIPE") != "" {
					invocationId = bb.besInvocationId
				}
				return cb(event, seqId, invocationId)
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
			p := p // capture
			eg.Go(func() error {
				if !p.Healthy() {
					return nil
				}

				// Channel for Send result
				sendCh := make(chan error, 1)

				// Run Send in goroutine
				go func() {
					err := p.Send(grpcEvent)
					sendCh <- err
				}()

				// Wait for Send or timeout
				select {
				case err := <-sendCh:
					if err != nil {
						p.MarkUnhealthy()
						bb.maybeAbortPipeBecauseNoHealthyBackends()
					}
					return nil
				case <-time.After(besSendTimeout):
					p.MarkUnhealthy()
					bb.maybeAbortPipeBecauseNoHealthyBackends()
					return nil
				}
			})
		}
	}

	return eg.Wait()
}

func (bb *besPipe) Args() []string {
	args := []string{
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
	bb.wg.Wait()

	os.Remove(bb.bepBinPath)
}
