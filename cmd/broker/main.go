package main

import (
	"os"
	"strconv"
	"time"

	"github.com/alexchomiak/broker/cmd/broker/metric"
	"github.com/alexchomiak/broker/cmd/broker/model"
	"github.com/alexchomiak/broker/cmd/broker/request"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/simplelru"
	"github.com/openzipkin/zipkin-go"
	zipkinmodel "github.com/openzipkin/zipkin-go/model"
	reporterhttp "github.com/openzipkin/zipkin-go/reporter/http"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	env := os.Getenv("ENV")

	var log *zap.Logger
	if env == "prod" {
		log, _ = zap.NewProduction()
	} else {
		log, _ = zap.NewDevelopment()
	}

	startup := log.Sugar().WithOptions(zap.Fields(zap.Field{
		Key:    "scope",
		Type:   zapcore.StringType,
		String: "service",
	}))

	metricPublisher := metric.NewMetricPublisher()

	startup.Info("Initializing broker service")
	app := fiber.New()

	var port string
	val, ok := os.LookupEnv("SERVICE_PORT")

	if ok {
		port = val
	} else {
		port = "8080"
	}

	// * Install prom metrics handler
	var metricsHandler fasthttp.RequestHandler
	metricsHandler = fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())

	app.Get("/metrics", func(c *fiber.Ctx) error {
		metricsHandler(c.Context())
		return nil
	})

	opsProcessed := promauto.NewCounter(prometheus.CounterOpts{
		Name: "myapp_processed_ops_total",
		Help: "The total number of processed events",
	})

	// * Bind middleware
	startup.Info("Binding middleware for Broker Service")
	// * Install PPROF middleware for profiling
	app.Use(pprof.New())

	app.Use(limiter.New(limiter.Config{
		Max:               30,
		Expiration:        1 * time.Minute,
		LimiterMiddleware: limiter.SlidingWindow{},
		LimitReached: func(c *fiber.Ctx) error {
			return c.JSON(&model.ErrorResponse{
				TimeStamp: time.Now(),
				Message:   "Too many requests. Maybe you should slow down? 🤓",
			})
		},
	}))

	// * Install ZIPKIN middleware for tracing
	// zipkinUrl := os.Getenv("ZIPKIN_ENDPOINT")
	// zipkinServiceName := os.Getenv("ZIPKIN_SERVICE_NAME")
	// zipkinServicePort := os.Getenv("ZIPKIN_SERVICE_HOST")

	log.Info("Initializing Zipkin Tracer")
	endpointUrl := "http://localhost:9411/api/v2/spans"
	localEndpoint, zerr := zipkin.NewEndpoint("broker", "localhost:9411")
	if zerr != nil {
		log.Fatal("Error initializing Zipkin Tracer", zap.Error(zerr))
	}
	reporter := reporterhttp.NewReporter(endpointUrl)
	sampler, zerr := zipkin.NewCountingSampler(1.0)
	if zerr != nil {
		log.Fatal("Error initializing Zipkin Tracer", zap.Error(zerr))
	}

	trace, zerr := zipkin.NewTracer(
		reporter,
		zipkin.WithLocalEndpoint(localEndpoint),
		zipkin.WithSampler(sampler),
	)
	if zerr != nil {
		log.Fatal("Error initializing Zipkin Tracer", zap.Error(zerr))
	}

	app.Use(func(c *fiber.Ctx) error {
		// * Start Span
		rspan, ctx := trace.StartSpanFromContext(c.UserContext(), c.Path(), zipkin.Kind(zipkinmodel.Server))
		c.Locals("tracer", trace)
		c.Locals("parentSpan", rspan)
		c.SetUserContext(ctx)
		c.Next()
		rspan.Finish()
		return nil
	})

	app.Use(requestid.New())

	// * Example of instance memory user cache
	// * using GO LRU to cache a uuid based off user IP
	// * though, this could be used to cache DB calls for User objects/etc
	// ! Note: when scaling this across multiple instances,
	// ! this cache will not be shared across instances
	// ! So it is best to use a Network load balancer/sticky sessions
	// ! or a shared cache like Redis
	cacheSizeStr := os.Getenv("CACHE_SIZE")
	var cacheSize int
	if cacheSizeStr != "" {
		cacheSizeInt64, _ := strconv.ParseInt(cacheSizeStr, 10, 64)
		cacheSize = int(cacheSizeInt64)
	} else {
		cacheSize = 100
	}

	userCache, _ := simplelru.NewLRU[string, string](int(cacheSize), nil)
	app.Use(func(c *fiber.Ctx) error {
		userSpan, _ := request.GetTracer(c).StartSpanFromContext(c.UserContext(), "resolveUser")
		ip := c.IP()
		var userId string
		if userCache.Contains(ip) {
			userId, _ = userCache.Get(ip)
		} else {
			userId = uuid.New().String()
			userCache.Add(ip, userId)
		}
		c.Locals("userId", userId)
		userSpan.Finish()
		return c.Next()
	})

	// * Logging Middleware using Zap
	app.Use(func(c *fiber.Ctx) error {
		createLoggerSpan, _ := request.GetTracer(c).StartSpanFromContext(c.UserContext(), "createLogger")

		logger := log.WithOptions(
			zap.Fields(
				zap.Field{
					Key:    "RequestID",
					Type:   zapcore.StringType,
					String: c.Locals("requestid").(string),
				},
				zap.Field{
					Key:    "method",
					Type:   zapcore.StringType,
					String: c.Method(),
				},
				zap.Field{
					Key:    "path",
					Type:   zapcore.StringType,
					String: c.Path(),
				},
				zap.Field{
					Key:    "scope",
					Type:   zapcore.StringType,
					String: "client-request",
				},
				zap.Field{
					Key:    "userId",
					Type:   zapcore.StringType,
					String: c.Locals("userId").(string),
				},
			),
		)

		c.Locals("logger", logger)
		createLoggerSpan.Finish()

		// * Request timing
		startTime := time.Now()

		infoSpan, _ := request.GetTracer(c).StartSpanFromContext(c.UserContext(), "infoLog")

		logger.Info("Request Started", zap.Time("startTime", startTime))
		infoSpan.Finish()
		c.Next()
		opsProcessed.Inc()

		metricPublisher.PublishCounter(metric.HttpRequestCountMetricName, c)
		metricPublisher.PublishHistogram(metric.HttpRequestDurationMetricName, c, float64(time.Since(startTime).Milliseconds()))
		metricPublisher.PublishHistogram(metric.HttpRequestDurationMicroMetricName, c, float64(time.Since(startTime).Microseconds()))

		logger.Info("Request Completed",
			zap.Int64("timeElapsedMillis", time.Since(startTime).Milliseconds()),
			zap.Int64("timeElapsedMicros", time.Since(startTime).Microseconds()),
		)

		return nil
	})

	app.Use(func(c *fiber.Ctx) error {
		// * Set Zipkin Parent Span tags
		span := c.Locals("parentSpan").(zipkin.Span)
		span.Tag("userId", c.Locals("userId").(string))
		span.Tag("requestId", c.Locals("requestid").(string))
		span.Tag("method", c.Method())
		span.Tag("path", c.Path())
		c.Next()
		span.Tag("status", strconv.FormatInt(int64(c.Response().StatusCode()), 10))
		return nil
	})

	startup.Info("Binding routes for Broker Service")

	app.Get("/health", func(c *fiber.Ctx) error {
		parentContext := c.UserContext()
		tracer := request.GetTracer(c)

		// * Over-kill example of tracing
		health, healthCtx := tracer.StartSpanFromContext(parentContext, "health")
		defer health.Finish()

		logTrace, _ := tracer.StartSpanFromContext(healthCtx, "get-logger")
		logr := request.GetLogger(c)
		logr.Debug("Creating health check response")
		logTrace.Finish()

		jsonTrace, _ := tracer.StartSpanFromContext(healthCtx, "marshal-json")
		c.JSON(&model.HealthCheckResponse{
			TimeStamp: time.Now(),
			Status:    "OK",
		})
		jsonTrace.Finish()

		return c.SendStatus(200)
	})

	startup.Info("Binding metrics for Broker service")
	metricPublisher.Initialize(app)

	startup.Info("Starting up the broker service")
	err := app.Listen(":" + port)
	if err != nil {
		startup.Error("Error starting up the broker service", err)
	}

}
