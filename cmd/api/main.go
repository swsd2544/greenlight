package main

import (
	"context"
	"database/sql"
	"expvar"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"greenlight.swsd2544.net/internal/data"
	"greenlight.swsd2544.net/internal/mailer"
	"greenlight.swsd2544.net/internal/vcs"
)

var version = vcs.Version()

type config struct {
	name string
	smtp struct {
		host     string
		username string
		password string
		sender   string
		port     int
	}
	env  string
	cors struct{ trustedOrigins []string }
	db   struct {
		dsn          string
		maxIdleTime  string
		maxOpenConns int
		maxIdleConns int
	}
	limiter struct {
		rps     float64
		burst   int
		enabled bool
	}
	otlp struct {
		enabled  bool
		endpoint string
	}
	port int
}

type application struct {
	wg     sync.WaitGroup
	models data.Models
	logger zerolog.Logger
	mailer mailer.Mailer
	config config
}

func main() {
	var cfg config

	flag.StringVar(&cfg.name, "name", "Greenlight", "Application's name")
	flag.IntVar(&cfg.port, "port", 4000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (development|staging|production)")
	flag.StringVar(&cfg.db.dsn, "db-dsn", "", "PostgreSQL DSN")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25, "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25, "PostgreSQL max idle connections")
	flag.StringVar(&cfg.db.maxIdleTime, "db-max-idle-time", "15m", "PostgreSQL max connection idle time")
	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", 2, "Rate limiter maximum requests per second")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", 4, "Rate limiter maximum burst")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", false, "Enable rate limiter")
	flag.StringVar(&cfg.smtp.host, "smtp-host", "smtp.mailtrap.io", "SMTP host")
	flag.IntVar(&cfg.smtp.port, "smtp-port", 25, "SMTP port")
	flag.StringVar(&cfg.smtp.username, "smtp-username", "18eac941f238e4", "SMTP username")
	flag.StringVar(&cfg.smtp.password, "smtp-password", "7c6aa2854f0ab1", "SMTP password")
	flag.StringVar(&cfg.smtp.sender, "smtp-sender", "Greenlight <no-reply@greenlight.swsd2544.net>", "SMTP sender")
	flag.BoolVar(&cfg.otlp.enabled, "otlp-enabled", false, "Enable OpenTelemetry")
	flag.StringVar(&cfg.otlp.endpoint, "otlp-endpoint", "localhost:4317", "OpenTelemetry Collector GRPC endpoint")
	flag.Func("cors-trusted-origins", "Trusted CORS origins (space seperated)", func(val string) error {
		cfg.cors.trustedOrigins = strings.Fields(val)
		return nil
	})

	displayVersion := flag.Bool("version", false, "Display version and exit")

	flag.Parse()

	if *displayVersion {
		fmt.Printf("Version:\t%s\n", version)
		os.Exit(0)
	}

	logger := zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()

	if cfg.otlp.enabled {
		exp, err := newOtelCollectorExporter(cfg)
		if err != nil {
			logger.Fatal().Err(err).Send()
		}
		tp := trace.NewTracerProvider(
			trace.WithSampler(trace.AlwaysSample()),
			trace.WithResource(newResource(cfg)),
			trace.WithSpanProcessor(trace.NewBatchSpanProcessor(exp)),
		)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := tp.Shutdown(ctx); err != nil {
				logger.Fatal().Err(err).Send()
			}
			if err := exp.Shutdown(ctx); err != nil {
				logger.Fatal().Err(err).Send()
			}
		}()
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		otel.SetTracerProvider(tp)
	}

	db, err := openDB(cfg)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to open database connection")
	}

	defer func() {
		err := db.Close()
		if err != nil {
			logger.Error().Err(err).Msg("failed to close database connection")
		}
	}()
	logger.Info().Msg("opened database connection pool")

	models := data.NewModels(db)
	mailerService := mailer.New(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username, cfg.smtp.password, cfg.smtp.sender)

	expvar.NewString("version").Set(version)
	expvar.Publish("goroutines", expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))
	expvar.Publish("database", expvar.Func(func() any {
		return db.Stats()
	}))
	expvar.Publish("timestamp", expvar.Func(func() any {
		return time.Now().Unix()
	}))

	app := &application{
		config: cfg,
		logger: logger,
		models: models,
		mailer: mailerService,
	}

	err = app.serve()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to serving the application")
	}
}

func openDB(cfg config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	duration, err := time.ParseDuration(cfg.db.maxIdleTime)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)
	db.SetConnMaxIdleTime(duration)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.PingContext(ctx)
	if err != nil {
		return nil, err
	}

	return db, nil
}
