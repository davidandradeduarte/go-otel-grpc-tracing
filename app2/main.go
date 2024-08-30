package main

// grpc server that listens on port 50051 and uses the person.proto protobuf /person/person.pb.go package person

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"app2/hello"
	"app2/person"

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
	"google.golang.org/grpc/credentials/insecure"
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
		sdktrace.WithBatcher(
			jaeger,
			sdktrace.WithMaxExportBatchSize(sdktrace.DefaultMaxExportBatchSize),
			sdktrace.WithBatchTimeout(sdktrace.DefaultScheduleDelay*time.Millisecond),
			sdktrace.WithMaxExportBatchSize(sdktrace.DefaultMaxExportBatchSize)),
		sdktrace.WithResource(
			sdkresource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String("app2"),
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
	tracer = tp.Tracer("app2")
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	person.RegisterPersonServiceServer(s, &server{})
	log.Printf("server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

type server struct {
	person.UnimplementedPersonServiceServer
}

func (s *server) SayHello(ctx context.Context, in *person.Person) (*person.Hello, error) {
	_, span := tracer.Start(ctx, "sayHello")
	defer span.End()
	log.Printf(`message="saying hello to %s (id: %s)" traceID=%s`, in.Name, in.Id, span.SpanContext().TraceID())

	time.Sleep(time.Second / 2)
	helloAddress := "localhost:50052"
	conn, err := grpc.NewClient(helloAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("could not connect to hello server: %v", err)
	}
	defer conn.Close()
	h := hello.NewHelloServiceClient(conn)
	hey, err := h.SayHello(ctx, &hello.Who{Name: in.Name})
	if err != nil {
		return nil, fmt.Errorf("could not send request to hello server: %v", err)
	}
	return &person.Hello{Message: hey.Message}, nil
}
