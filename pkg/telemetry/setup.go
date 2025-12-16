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
	"fmt"
	"os"

	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkTrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	// "go.opentelemetry.io/collector/exporter/otlphttpexporter"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
)

const (
	outputFileEnv = "ASPECT_OTEL_OUT"
	endpointEnv   = "ASPECT_OTEL_ENDPOINT"
)

/**
 * Configure global OpenTelemetry settings for the CLI.
 */
func StartSession(ctx context.Context) func() {
	telemetryOutFile := os.Getenv(outputFileEnv)
	if telemetryOutFile == "" {
		telemetryOutFile = viper.GetString("telemetry.output")
	}
	if telemetryOutFile != "" {
		des, err := setupOTelFileTracer(ctx, telemetryOutFile)
		if err != nil {
			panic(err)
		}
		return des
	}

	telemetryEndpoint := os.Getenv(endpointEnv)
	if telemetryEndpoint == "" {
		telemetryEndpoint = viper.GetString("telemetry.endpoint")
	}
	if telemetryEndpoint != "" {
		des, err := setupOTelOTLP(ctx, telemetryEndpoint)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Sending telemetry to OTLP server at %s\n", telemetryEndpoint)
		return des
	}

	// No telemetry configured
	return func() {}
}

func setupOTelTracer(ctx context.Context, tracer sdkTrace.SpanExporter) (func(), error) {
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

	tp := sdkTrace.NewTracerProvider(
		sdkTrace.WithBatcher(tracer),
		sdkTrace.WithResource(r),
	)

	otel.SetTracerProvider(tp)

	return func() {
		tp.Shutdown(ctx)
	}, nil
}

func setupOTelFileTracer(ctx context.Context, telemetryOutFile string) (func(), error) {
	f, err := os.OpenFile(telemetryOutFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	exp, err := stdouttrace.New(stdouttrace.WithWriter(f))
	if err != nil {
		return nil, err
	}

	return setupOTelTracer(ctx, exp)
}

func setupOTelOTLP(ctx context.Context, telemetryEndpoint string) (func(), error) {
	exp, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpoint(telemetryEndpoint),
	)
	if err != nil {
		panic(err)
	}

	// exp, err := otlphttpexporter.NewFactory().CreateTraces(ctx, nil, otlphttpexporter.WithEndpoint(telemetryEndpoint))

	startErr := exp.Start(ctx)
	if startErr != nil {
		return nil, startErr
	}

	return setupOTelTracer(ctx, exp)
}
