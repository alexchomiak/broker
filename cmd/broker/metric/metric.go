package metric

import (
	"fmt"
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	// * Metric names
	HttpRequestCountMetricName         = "http_request_count"
	HttpRequestDurationMetricName      = "http_request_duration_ms"
	HttpRequestDurationMicroMetricName = "http_request_duration_micro"
)

type MetricPublisher struct {
	metricNameRegex *regexp.Regexp
	metricMap       map[string]any
}

func NewMetricPublisher() *MetricPublisher {
	return &MetricPublisher{
		metricNameRegex: regexp.MustCompile("[^_A-Za-z]+"),
		metricMap:       make(map[string]any),
	}
}

func (mc *MetricPublisher) GetMetricName(ctx *fiber.Ctx) string {
	return mc.metricNameRegex.ReplaceAllString(
		fmt.Sprintf("%s_%s", ctx.Method(), ctx.Path()),
		"",
	)
}

func (mc *MetricPublisher) PublishCounter(prefix string, ctx *fiber.Ctx) {
	key := fmt.Sprintf("%s_%s", prefix, mc.GetMetricName(ctx))
	m, ok := mc.metricMap[key]
	if !ok {
		return
	}
	m.(prometheus.Counter).Inc()
}

func (mc *MetricPublisher) GetHistogram(prefix string, ctx *fiber.Ctx, value float64) {
	key := fmt.Sprintf("%s_%s", prefix, mc.GetMetricName(ctx))
	m, ok := mc.metricMap[key]
	if !ok {
		return
	}
	m.(prometheus.Histogram).Observe(value)
}

func (mc *MetricPublisher) InsertHistogram(
	metricName string,
	metricDescription string,
	buckets []float64,
) {
	mc.metricMap[metricName] = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    metricName,
		Help:    metricDescription,
		Buckets: buckets,
	})
}

func (mc *MetricPublisher) InsertCounter(metricName string, metricDescription string) {
	mc.metricMap[metricName] = promauto.NewCounter(prometheus.CounterOpts{
		Name: metricName,
		Help: metricDescription,
	})
}

func (mc *MetricPublisher) Initialize(app *fiber.App) {
	routes := app.GetRoutes()
	for _, route := range routes {
		key := route.Method + "_" + route.Path
		postfix := mc.metricNameRegex.ReplaceAllString(key, "")

		// * Install route metrics
		mc.InsertCounter(fmt.Sprintf("%s_%s", HttpRequestCountMetricName, postfix), fmt.Sprintf("Number of HTTP requests for %s", key))
		mc.InsertHistogram(
			fmt.Sprintf("%s_%s", HttpRequestDurationMetricName, postfix),
			fmt.Sprintf("Duration of HTTP requests for %s", key),
			[]float64{1, 2, 4, 8, 10, 25, 50, 100, 250, 500, 1000},
		)
	}
}
