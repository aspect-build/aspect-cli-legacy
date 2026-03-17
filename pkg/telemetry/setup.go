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

	"github.com/aspect-build/aspect-cli-legacy/buildinfo"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

const (
	// Env to opt-in to file-based telemetry output. Overrides other telemetry settings.
	outputFileEnv = "ASPECT_OTEL_OUT"

	// Env to opt-in to OTLP exporter endpoint. Overrides other telemetry settings.
	// Additional OTLP may be set via environment variables as per:
	// https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/#endpoint-configuration
	endpointEnv = "ASPECT_OTEL_ENDPOINT"
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
		// Headers can be set via config file.
		// Additional OTLP may be set via environment variables as per:
		// https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/#endpoint-configuration
		headers := viper.GetStringMapString("telemetry.headers")

		des, err := setupOTelOTLP(ctx, telemetryEndpoint, headers)
		if err != nil {
			panic(err)
		}
		return des
	}

	// No telemetry configured
	return func() {}
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

func setupOTelOTLP(ctx context.Context, telemetryEndpointUrl string, headers map[string]string) (func(), error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(telemetryEndpointUrl),
	}
	if len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	exp := otlptracehttp.NewUnstarted(opts...)

	err := exp.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlptracehttp start error: %w", err)
	}

	return setupOTelTracer(ctx, exp)
}

func setupOTelTracer(ctx context.Context, exp trace.SpanExporter) (func(), error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName("Aspect CLI"),
		semconv.ServiceVersion(buildinfo.Current().Version()),
	}
	if wd, err := os.Getwd(); err == nil {
		attrs = append(attrs, semconv.ProcessWorkingDirectory(wd))
	}

	r, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithOSType(),
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessOwner(),
		resource.WithProcessRuntimeVersion(),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(r),
	)

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		fmt.Fprintf(os.Stderr, "otel internal error: %v\n", err)
	}))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func() {
		err := tp.ForceFlush(ctx)
		if err != nil {
			panic(err)
		}
		err = tp.Shutdown(ctx)
		if err != nil {
			panic(err)
		}
	}, nil
}
