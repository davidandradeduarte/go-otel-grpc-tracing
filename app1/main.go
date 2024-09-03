package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"app1/person"

	"github.com/redpanda-data/benthos/v4/public/service"
	_ "github.com/redpanda-data/connect/public/bundle/free/v4"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	time.Sleep(3 * time.Second)
	service.RunCLI(context.Background())
}

func init() {
	if err := service.RegisterProcessor(
		"grpc",
		service.NewConfigSpec().
			Summary("sends the message to a gRPC server").
			Field(service.NewStringField("address")),
		newProcessor,
	); err != nil {
		panic(err)
	}
}

type processor struct {
	logger  *service.Logger
	tracer  trace.Tracer
	address string
}

func newProcessor(conf *service.ParsedConfig, mgr *service.Resources) (service.Processor, error) {
	addr, err := conf.FieldString("address")
	if err != nil {
		return nil, fmt.Errorf("error retrieving address: %v", err)
	}
	tracer := mgr.OtelTracer().Tracer("benthos")
	return &processor{
		logger:  mgr.Logger(),
		tracer:  tracer,
		address: addr,
	}, nil
}

type message struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (p *processor) Process(_ context.Context, msg *service.Message) (service.MessageBatch, error) {
	conn, err := grpc.NewClient(p.address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("could not connect to person server: %v", err)
	}
	defer conn.Close()
	psc := person.NewPersonServiceClient(conn)
	bytes, err := msg.AsBytes()
	if err != nil {
		return nil, fmt.Errorf("error converting message to bytes: %v", err)
	}
	var m message
	if err := json.Unmarshal(bytes, &m); err != nil {
		return nil, fmt.Errorf("failed to convert message to struct")
	}
	h, err := psc.SayHello(msg.Context(), &person.Person{Id: m.ID})
	if err != nil {
		return nil, fmt.Errorf("could not send request to person server: %v", err)
	}
	msg.SetBytes([]byte(h.Message))

	return service.MessageBatch{msg}, nil
}

func (p *processor) Close(_ context.Context) error {
	return nil
}
