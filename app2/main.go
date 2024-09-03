package main

// grpc server that listens on port 50051 and uses the person.proto protobuf /person/person.pb.go package person

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"time"

	"app2/hello"
	"app2/person"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"

	_ "github.com/lib/pq" // postgres driver"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	tracer trace.Tracer
	db     *sql.DB
)

func init() {
	db = initDB()
	seed()
}

func initTracerProvider() *sdktrace.TracerProvider {
	otlpExporter, err := otlptrace.New(
		context.Background(),
		otlptracegrpc.NewClient(
			otlptracegrpc.WithEndpoint("jaeger:4317"),
			otlptracegrpc.WithInsecure(),
		),
	)
	if err != nil {
		log.Fatalf("error creating otlp exporter: %v", err)
		return nil
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(otlpExporter),
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
	lis, err := net.Listen("tcp", ":50052")
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
	log.Printf(`message="getting name for person id: %s" traceID=%s`, in.Id, span.SpanContext().TraceID())

	row := db.QueryRowContext(ctx, "SELECT name FROM person WHERE id = $1", in.Id)
	if err := row.Err(); err != nil {
		return nil, fmt.Errorf("error getting name from db: %v", err)
	}
	var name string
	if err := row.Scan(&name); err != nil {
		return nil, fmt.Errorf("error scanning row: %v", err)
	}

	time.Sleep(time.Second / 2)
	helloAddress := "app3:50053"
	conn, err := grpc.NewClient(helloAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("could not connect to hello server: %v", err)
	}
	defer conn.Close()
	h := hello.NewHelloServiceClient(conn)
	hey, err := h.SayHello(ctx, &hello.Who{Name: name})
	if err != nil {
		return nil, fmt.Errorf("could not send request to hello server: %v", err)
	}
	return &person.Hello{Message: hey.Message}, nil
}

func initDB() *sql.DB {
	var (
		err error
		db  *sql.DB
	)
	timeout := time.After(10 * time.Second)
	retryInterval := 500 * time.Millisecond
	for {
		select {
		case <-timeout:
			panic(fmt.Errorf("timeout waiting for db to be ready: %v", err))
		default:
			db, err = otelsql.Open(
				"postgres", "postgres://postgres:postgres@db:5432/postgres?sslmode=disable",
				otelsql.WithAttributes(
					semconv.DBSystemPostgreSQL,
				))
			if err != nil {
				break
			}
			_, err = db.Exec(`SELECT 1`)
			if err == nil {
				return db
			}
		}
		time.Sleep(retryInterval)
	}
}

func seed() {
	var err error
	_, err = db.Exec(`
					CREATE TABLE IF NOT EXISTS person (
						id VARCHAR(255) PRIMARY KEY,
						name VARCHAR(255)
					);
				`)
	if err != nil {
		panic(fmt.Errorf("error creating table: %v", err))
	}
	_, err = db.Exec(`
					INSERT INTO person (id, name) VALUES ('1', 'David');
				`)
	if err != nil {
		panic(fmt.Errorf("error seeding table: %v", err))
	}
}
