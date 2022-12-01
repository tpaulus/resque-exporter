package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
)

const (
	namespace = "resque"
)

var (
	promRegistry *prometheus.Registry

	ctx            = context.Background()
	redisNamespace = flag.String(
		"redis.namespace",
		"resque",
		"Namespace used by Resque to prefix all its Redis keys.",
	)
	redisURL = flag.String(
		"redis.url",
		"redis://localhost:6379",
		"URL to the Redis backing the Resque.",
	)
	printVersion = flag.Bool(
		"version",
		false,
		"Print version information.",
	)
	listenAddress = flag.String(
		"web.listen-address",
		":9447",
		"Address to listen on for web interface and telemetry.",
	)
	metricPath = flag.String(
		"web.telemetry-path",
		"/metrics",
		"Path under which to expose metrics.",
	)
)

var (
	failedJobExecutionsDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "failed_job_executions_total"),
		"Total number of failed job executions.",
		nil, nil,
	)
	jobExecutionsDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "job_executions_total"),
		"Total number of job executions.",
		nil, nil,
	)
	jobsInFailedQueueDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "jobs_in_failed_queue"),
		"Number of jobs in a failed queue.",
		[]string{"queue"}, nil,
	)
	jobsInQueueDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "jobs_in_queue"),
		"Number of jobs in a queue.",
		[]string{"queue"}, nil,
	)
	processingRatioDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "processing_ratio"),
		"Ratio of queued jobs to workers processing those queues.",
		[]string{"queue"}, nil,
	)
	scrapeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "scrape_duration_seconds"),
		"Time this scrape of resque metrics took.",
		nil, nil,
	)
	upDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "up"),
		"Whether this scrape of resque metrics was successful.",
		nil, nil,
	)
	workersDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "workers"),
		"Number of workers.",
		nil, nil,
	)
	workersPerQueueDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "workers_per_queue"),
		"Number of workers handling a specific queue.",
		[]string{"queue"}, nil,
	)
	workingWorkersDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "working_workers"),
		"Number of working workers.",
		nil, nil,
	)
)

// Exporter collects Resque metrics. It implements prometheus.Collector.
type Exporter struct {
	redisClient    *redis.Client
	redisNamespace string

	failedScrapes prometheus.Counter
	scrapes       prometheus.Counter
}

// NewExporter returns a new Resque exporter.
func NewExporter(redisURL, redisNamespace string) (*Exporter, error) {
	redisClient, err := newRedisClient(redisURL)
	if err != nil {
		return nil, err
	}

	return &Exporter{
		redisClient:    redisClient,
		redisNamespace: redisNamespace,
		failedScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "failed_scrapes_total",
			Help:      "Total number of failed scrapes.",
		}),
		scrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scrapes_total",
			Help:      "Total number of scrapes.",
		}),
	}, nil
}

func newRedisClient(redisURL string) (*redis.Client, error) {
	var options redis.Options

	u, err := url.Parse(redisURL)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "redis" || u.Scheme == "tcp" {
		options.Network = "tcp"
		options.Addr = net.JoinHostPort(u.Hostname(), u.Port())
		if len(u.Path) > 1 {
			if db, err := strconv.Atoi(u.Path[1:]); err == nil {
				options.DB = db
			}
		}
	} else if u.Scheme == "unix" {
		options.Network = "unix"
		options.Addr = u.Path
	} else {
		return nil, fmt.Errorf("unknown URL scheme: %s", u.Scheme)
	}

	if password, ok := u.User.Password(); ok {
		options.Password = password
	}

	return redis.NewClient(&options), nil
}

// Describe implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- failedJobExecutionsDesc
	ch <- jobExecutionsDesc
	ch <- jobsInFailedQueueDesc
	ch <- jobsInQueueDesc
	ch <- scrapeDurationDesc
	ch <- upDesc
	ch <- workersDesc
	ch <- workingWorkersDesc

	ch <- e.failedScrapes.Desc()
	ch <- e.scrapes.Desc()
}

// Collect implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	if err := e.scrape(ch); err != nil {
		e.failedScrapes.Inc()
		log.Error(err)
		ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, 0)
	} else {
		ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, 1)
	}

	ch <- e.failedScrapes
	ch <- e.scrapes
}

func (e *Exporter) scrape(ch chan<- prometheus.Metric) error {
	e.scrapes.Inc()

	defer func(start time.Time) {
		ch <- prometheus.MustNewConstMetric(
			scrapeDurationDesc,
			prometheus.GaugeValue,
			float64(time.Since(start).Seconds()))
	}(time.Now())

	executions, err := e.redisClient.Get(ctx, e.redisKey("stat:processed")).Float64()
	if err != nil && err != redis.Nil {
		return err
	}
	ch <- prometheus.MustNewConstMetric(jobExecutionsDesc, prometheus.CounterValue, executions)

	failedExecutions, err := e.redisClient.Get(ctx, e.redisKey("stat:failed")).Float64()
	if err != nil && err != redis.Nil {
		return err
	}
	ch <- prometheus.MustNewConstMetric(failedJobExecutionsDesc, prometheus.CounterValue, failedExecutions)

	queues, err := e.redisClient.SMembers(ctx, e.redisKey("queues")).Result()
	if err != nil {
		return err
	}

	workers, err := e.redisClient.SMembers(ctx, e.redisKey("workers")).Result()
	if err != nil {
		return err
	}
	ch <- prometheus.MustNewConstMetric(workersDesc, prometheus.GaugeValue, float64(len(workers)))

	workersPerQueue := make(map[string]int64)
	var workingWorkers int
	for _, worker := range workers {
		exists, err := e.redisClient.Exists(ctx, e.redisKey("worker", worker)).Result()
		if err != nil {
			return err
		}
		if exists == 1 {
			workingWorkers++
		}

		workerDetails := strings.Split(worker, ":")
		workerQueuesCsv := workerDetails[len(workerDetails)-1]
		workerQueues := strings.Split(workerQueuesCsv, ",")

		// Determine if worker is handling all queues
		allQueues := false
		for _, queue := range workerQueues {
			if queue == "*" {
				allQueues = true
				break
			}
		}

		if allQueues {
			workerQueues = queues
		}

		for _, queue := range workerQueues {
			workersPerQueue[queue]++
		}
	}
	ch <- prometheus.MustNewConstMetric(workingWorkersDesc, prometheus.GaugeValue, float64(workingWorkers))

	processingRatioPerQueue := make(map[string]float64)
	for _, queue := range queues {
		jobs, err := e.redisClient.LLen(ctx, e.redisKey("queue", queue)).Result()
		if err != nil {
			return err
		}

		// Ensure ratio is useful when number of workers is zero.
		// For example, if there are 10 queued jobs we would want a processing ratio
		// of 10 as opposed to 0. This is important when scaling to zero is required.
		normalizedWorkersPerQueue := workersPerQueue[queue]
		if normalizedWorkersPerQueue == 0 {
			normalizedWorkersPerQueue = 1
		}

		processingRatioPerQueue[queue] = float64(jobs) / float64(normalizedWorkersPerQueue)
		ch <- prometheus.MustNewConstMetric(jobsInQueueDesc, prometheus.GaugeValue, float64(jobs), queue)
		ch <- prometheus.MustNewConstMetric(processingRatioDesc, prometheus.GaugeValue, float64(processingRatioPerQueue[queue]), queue)
		ch <- prometheus.MustNewConstMetric(workersPerQueueDesc, prometheus.GaugeValue, float64(workersPerQueue[queue]), queue)
	}

	failedQueues, err := e.redisClient.SMembers(ctx, e.redisKey("failed_queues")).Result()
	if err != nil {
		return err
	}

	if len(failedQueues) == 0 {
		exists, err := e.redisClient.Exists(ctx, e.redisKey("failed")).Result()
		if err != nil {
			return err
		}
		if exists == 1 {
			failedQueues = []string{"failed"}
		}
	}

	for _, queue := range failedQueues {
		jobs, err := e.redisClient.LLen(ctx, e.redisKey(queue)).Result()
		if err != nil {
			return err
		}
		ch <- prometheus.MustNewConstMetric(jobsInFailedQueueDesc, prometheus.GaugeValue, float64(jobs), queue)
	}

	return nil
}

func (e *Exporter) redisKey(a ...string) string {
	return e.redisNamespace + ":" + strings.Join(a, ":")
}

func init() {
	promRegistry = prometheus.NewRegistry()
	promRegistry.MustRegister(version.NewCollector("resque_exporter"))
}

func main() {
	flag.Parse()

	if *printVersion {
		fmt.Println(version.Print("resque-exporter"))
		return
	}

	log.Infoln("Starting resque-exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	if u := os.Getenv("REDIS_URL"); len(u) > 0 {
		*redisURL = u
	}

	exporter, err := NewExporter(*redisURL, *redisNamespace)
	if err != nil {
		log.Fatal(err)
	}
	promRegistry.MustRegister(exporter)

	http.Handle(*metricPath, promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{}))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
<head><title>Resque Exporter</title></head>
<body>
<h1>Resque Exporter</h1>
<p><a href='` + *metricPath + `'>Metrics</a></p>
</body>
</html>
`))
	})

	log.Infoln("Listening on", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
