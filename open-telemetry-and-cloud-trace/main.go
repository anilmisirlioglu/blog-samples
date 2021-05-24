package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	texporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/api/option"
)

type Repository struct {
	Id              int    `json:"id"`
	StargazersCount int    `json:"stargazers_count"`
	Forks           int    `json:"forks"`
	Name            string `json:"name"`
	FullName        string `json:"full_name"`
}

var (
	httpClient = &http.Client{Timeout: 5 * time.Second}
	tracer     trace.Tracer
)

func main() {
	initTracer()

	serveHTTP()
}

func initTracer() {
	exporter, err := texporter.NewExporter(texporter.WithTraceClientOptions([]option.ClientOption{
		option.WithTelemetryDisabled(),
	}))
	if err != nil {
		log.Fatalf("texporter.NewExporter: %v", err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter,
		sdktrace.WithBatchTimeout(time.Second),
		sdktrace.WithMaxExportBatchSize(16)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(resource.NewWithAttributes(
			attribute.String("service.name", "sample-service"),
			attribute.String("service.version", "1.0.0"),
			attribute.String("instance.id", "foo12345"),
		)),
	)
	otel.SetTracerProvider(tp)

	tracer = otel.GetTracerProvider().Tracer("company.com/trace")
	defer func() {
		if err := tp.ForceFlush(context.Background()); err != nil {
			log.Println("failed to flush tracer")
		}
	}()
}

func serveHTTP() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("defaulting to port %s", port)
	}

	http.HandleFunc("/run", handler)

	log.Printf("server starting at: %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
	subCtx, span := tracer.Start(r.Context(), "/run")
	span.AddEvent("handling request")

	repo := new(Repository)
	err := fetchJSON(subCtx, "https://api.github.com/repos/golang/go", repo)

	_, wSpan := tracer.Start(subCtx, "write")
	defer wSpan.End()

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(io.MultiWriter(os.Stderr, w), "error: %v\n", err)
		return
	}

	span.SetAttributes(
		attribute.String("golang.go.repo.name", repo.FullName),
		attribute.Int("golang.go.repo.id", repo.Id),
		attribute.Int("golang.go.repo.stars", repo.StargazersCount),
	)

	_, _ = fmt.Fprintf(w,
		"===== %s =====\nRepository: %s (ID: %d)\nStar Count: %d\nFork Count: %d\nURL: https://github.com/%s",
		repo.FullName, repo.Name, repo.Id, repo.StargazersCount, repo.Forks, repo.FullName,
	)
	span.End()
}

func fetchJSON(ctx context.Context, url string, target interface{}) error {
	subCtx, span := tracer.Start(ctx, "fetch json")
	_, cancel := context.WithTimeout(subCtx, time.Second * 3)
	defer cancel()
	span.AddEvent("fetching repo info from github")
	r, err := httpClient.Get(url)

	if err != nil {
		return err
	}

	span.SetAttributes(attribute.KeyValue{
		Key:   "github.resp.status.code",
		Value: attribute.IntValue(r.StatusCode),
	})
	span.End()

	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(r.Body)

	_, span = tracer.Start(ctx, "parse json")
	defer span.End()
	return json.NewDecoder(r.Body).Decode(target)
}