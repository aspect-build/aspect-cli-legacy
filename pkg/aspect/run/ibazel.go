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
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/aspect-build/aspect-gazelle/runner/pkg/ibp"
)

var (
	SHUTDOWN_KILL_DELAY = 5 * time.Second
)

// A IncrementalBazel implementation that communicates with the ibazel protocol.
type IBazelProtocol struct {
	// This can be set to nil to
	stdin io.WriteCloser
}

var _ ibp.IncrementalBazel = (*IBazelProtocol)(nil)

func (ib *IBazelProtocol) HasConnection() bool {
	return ib.stdin != nil
}

func (ib *IBazelProtocol) Init(ctx context.Context, scope ibp.WatchScope, sources ibp.SourceInfoMap) error {
	return nil
}
func (ib *IBazelProtocol) Cycle(ctx context.Context, scope ibp.WatchScope, changes ibp.SourceInfoMap) error {
	res := ib.buildOne(true)

	// Add some delay to let the filesystem settle before we can exit the build state.
	// In the future we might make this configurable.
	time.Sleep(100 * time.Millisecond)

	return res
}
func (ib *IBazelProtocol) Close() error {
	return nil
}
func (ib *IBazelProtocol) Exit(ctx context.Context, err error) error {
	return ib.buildOne(err == nil)
}

func (ib *IBazelProtocol) Address() string {
	return ""
}
func (ib *IBazelProtocol) Env() []string {
	return []string{}
}
func (ib *IBazelProtocol) Serve(ctx context.Context) error {
	return nil
}
func (ib *IBazelProtocol) WaitForConnection() <-chan ibp.ProtocolVersion {
	return nil
}
func (rb *IBazelProtocol) WatchingScope(cap ibp.WatchScope) bool {
	return cap == ibp.WatchScope_Runfiles
}

func (events *IBazelProtocol) write(data string) error {
	if events.stdin == nil {
		return nil
	}
	bytes := []byte(data)
	bytes = append(bytes, []byte("\n")...)
	_, err := events.stdin.Write(bytes)
	if err != nil {
		return err
	}
	return nil
}

func (events *IBazelProtocol) buildStart() error {
	return events.write("IBAZEL_BUILD_STARTED")
}

func (events *IBazelProtocol) buildEnd(success bool) error {
	if success {
		return events.write("IBAZEL_BUILD_COMPLETED SUCCESS")
	}
	return events.write("IBAZEL_BUILD_COMPLETED ERROR")
}

// Its same as running buildStart and buildEnd back to back.
func (events *IBazelProtocol) buildOne(success bool) error {
	if err := events.buildStart(); err != nil {
		return err
	}
	if err := events.buildEnd(success); err != nil {
		return err
	}
	return nil
}

// A IncrementalBazel implementation that restarts the process
type RestartBazelProtocol struct {
	createRunCmd func() *exec.Cmd
	runCmd       *exec.Cmd
}

var _ ibp.IncrementalBazel = (*RestartBazelProtocol)(nil)

func (rb *RestartBazelProtocol) start() error {
	if rb.runCmd != nil {
		return fmt.Errorf("already running, cannot start again")
	}

	runCmd := rb.createRunCmd()
	if err := runCmd.Start(); err != nil {
		return fmt.Errorf("failed to start the process: %w", err)
	}

	rb.runCmd = runCmd
	return nil
}

func (rb *RestartBazelProtocol) kill() error {
	if rb.runCmd == nil {
		return nil
	}

	// ignore the error from terminate(), we don't care if the process exited cleanly or not.
	p := rb.runCmd.Process
	rb.runCmd = nil
	return terminate(p)
}

func (rb *RestartBazelProtocol) HasConnection() bool {
	return false
}
func (rb *RestartBazelProtocol) Init(ctx context.Context, scope ibp.WatchScope, sources ibp.SourceInfoMap) error {
	return nil
}
func (rb *RestartBazelProtocol) Cycle(ctx context.Context, scope ibp.WatchScope, changes ibp.SourceInfoMap) error {
	if err := rb.kill(); err != nil {
		return fmt.Errorf("failed to close the previous process: %w", err)
	}

	if err := rb.start(); err != nil {
		return fmt.Errorf("failed to start the run process: %w", err)
	}

	// Add some delay to let the filesystem settle before we can exit the build state.
	// In the future we might make this configurable.
	time.Sleep(100 * time.Millisecond)

	return nil
}
func (rb *RestartBazelProtocol) Close() error {
	return nil
}
func (rb *RestartBazelProtocol) Exit(ctx context.Context, err error) error {
	return nil
}

func (ib *RestartBazelProtocol) Serve(ctx context.Context) error {
	return nil
}
func (ib *RestartBazelProtocol) Address() string {
	return ""
}
func (ib *RestartBazelProtocol) Env() []string {
	return []string{}
}
func (ib *RestartBazelProtocol) WaitForConnection() <-chan ibp.ProtocolVersion {
	return nil
}
func (rb *RestartBazelProtocol) WatchingScope(cap ibp.WatchScope) bool {
	return cap == ibp.WatchScope_Runfiles
}

func terminate(p *os.Process) error {
	if p == nil {
		return nil
	}

	err := p.Signal(syscall.SIGTERM)
	done := make(chan bool, 1)
	go func() {
		select {
		case <-time.After(SHUTDOWN_KILL_DELAY):
			err = kill(p)
		case <-done:
			// The subprocess was terminated with SIGTERM
		}
	}()
	p.Wait()
	done <- true
	return err
}

func kill(p *os.Process) error {
	return p.Signal(syscall.SIGKILL)
}
