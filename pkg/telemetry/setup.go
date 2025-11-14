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
	"context"
	"os"

	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
)

const (
	outputFileEnv = "ASPECT_OTEL_OUT"
)

/**
 * Configure global OpenTelemetry settings for the CLI.
 */
func StartSession(ctx context.Context) func() {
	des, err := setupOTelFile(ctx)
	if err != nil {
		panic(err)
	}
	if des == nil {
		return func() {}
	}
	return des
}

func setupOTelFile(ctx context.Context) (func(), error) {
	telemetryOutFile := os.Getenv(outputFileEnv)
	if telemetryOutFile == "" {
		telemetryOutFile = viper.GetString("telemetry.output")
	}

	// No telemetry output configured
	if telemetryOutFile == "" {
		return nil, nil
	}

	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("Aspect CLI"),
		),
	)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(telemetryOutFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	exp, err := stdouttrace.New(stdouttrace.WithWriter(f))
	if err != nil {
		return nil, err
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(r),
	)

	otel.SetTracerProvider(tp)

	return func() {
		tp.Shutdown(ctx)
	}, nil
}
