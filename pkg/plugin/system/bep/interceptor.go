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
	"context"

	"github.com/aspect-build/aspect-cli-legacy/pkg/plugin/system/besproxy"
)

type BESInterceptor interface {
	// Start anything needed in the background for the lifetime of the interceptor.
	ServeWait(ctx context.Context) error

	// Stop and cleanup.
	GracefulStop()

	// Args added to the bazel command line.
	Args() []string

	Errors() []error

	RegisterBesProxy(ctx context.Context, p besproxy.BESProxy)

	RegisterSubscriber(callback CallbackFn, multiThreaded bool)
}
