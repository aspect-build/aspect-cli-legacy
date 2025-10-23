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

import "context"

type bepInterceptorKeyType string

const (
	besInterceptorKey bepInterceptorKeyType = "aspect:bepInterceptor"
)

func BESErrors(ctx context.Context) []error {
	if HasBESInterceptor(ctx) {
		return BESInterceptorFromContext(ctx).Errors()
	}
	return nil
}

func HasInterceptor(ctx context.Context) bool {
	v := ctx.Value(besInterceptorKey)
	return v != nil
}

// InjectBESInterceptor injects the given BESInterceptor into the context.
func InjectBESInterceptor(ctx context.Context, besInterceptor BESInterceptor) context.Context {
	return context.WithValue(ctx, besInterceptorKey, besInterceptor)
}

// InjectBESInterceptor injects the given BESInterceptor into the context.
func BESInterceptorFromContext(ctx context.Context) BESInterceptor {
	return ctx.Value(besInterceptorKey).(BESInterceptor)
}

func HasBESInterceptor(ctx context.Context) bool {
	_, ok := ctx.Value(besInterceptorKey).(BESInterceptor)
	return ok
}
