package main

// grpc server that listens on port 50052 and uses the hello.proto protobuf /hello/hello.pb.go package hello
// to build a message to greet a person

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"app3/hello"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

var tracer trace.Tracer

func initTracerProvider() *sdktrace.TracerProvider {
	headers := map[string]string{
		"content-type": "application/json",
	}
	jaeger, err := otlptrace.New(
		context.Background(),
		otlptracehttp.NewClient(
			otlptracehttp.WithEndpoint("localhost:4318"),
			otlptracehttp.WithHeaders(headers),
			otlptracehttp.WithInsecure(),
		),
	)
	if err != nil {
		log.Fatalf("new jaeger exporter failed: %v", err)
		return nil
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(jaeger),
		sdktrace.WithResource(
			sdkresource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String("app3"),
			),
		),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp
}
func main() {
	tp := initTracerProvider()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()
	tracer = tp.Tracer("app3")
	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	hello.RegisterHelloServiceServer(s, &server{})
	log.Printf("server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

type server struct {
	hello.UnimplementedHelloServiceServer
}

func (s *server) SayHello(ctx context.Context, in *hello.Who) (*hello.Hello, error) {
	_, span := tracer.Start(ctx, "sayHello")
	defer span.End()
	log.Printf(`message="saying hello to %s" traceID=%s`, in.Name, span.SpanContext().TraceID())
	time.Sleep(time.Second)
	return &hello.Hello{Message: fmt.Sprintf("Hello, %s!", in.Name)}, nil
}
