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
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
)

const (
	// Env to opt-in to file-based telemetry output. Overrides other telemetry settings.
	outputFileEnv = "ASPECT_OTEL_OUT"

	// Env to opt-in to OTLP exporter endpoint. Overrides other telemetry settings.
	// Additional OTLP may be set via environment variables as per:
	// https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/#endpoint-configuration
	endpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"
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
	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("Aspect CLI"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(r),
	)

	otel.SetTracerProvider(tp)

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
