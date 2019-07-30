package basic

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"

	"github.com/percona/rds_exporter/config"
	"github.com/percona/rds_exporter/sessions"
)

var (
	scrapeTimeDesc = prometheus.NewDesc(
		"rds_exporter_scrape_duration_seconds",
		"Time this RDS scrape took, in seconds.",
		[]string{},
		nil,
	)
)

// OverlappingMetrics flag.
type OverlappingMetrics bool

const (
	// EnableOverlapping flag for enabling overlapping version.
	EnableOverlapping OverlappingMetrics = true
	// DisableOverlapping flag for disabling overlapping version.
	DisableOverlapping OverlappingMetrics = false
)

type Metric struct {
	Name string
	Desc *prometheus.Desc
}

type Exporter struct {
	config   *config.Config
	sessions *sessions.Sessions
	metrics  []Metric
	l        log.Logger
}

// New creates a new instance of a Exporter.
// enableOverlapping is using for backward compatibility.
// See: https://jira.percona.com/browse/PMM-1901.
func New(config *config.Config, sessions *sessions.Sessions, enableMetrics OverlappingMetrics) *Exporter {
	var m []Metric
	m = append(m, Metrics...)
	if enableMetrics {
		m = append(m, MetricsOverlappingWithEnhancedCollector...)
	}

	return &Exporter{
		config:   config,
		sessions: sessions,
		metrics:  m,
		l:        log.With("component", "basic"),
	}
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	e.collect(ch)

	// Collect scrape time
	ch <- prometheus.MustNewConstMetric(scrapeTimeDesc, prometheus.GaugeValue, time.Since(now).Seconds())
}

func (e *Exporter) collect(ch chan<- prometheus.Metric) {
	wg := &sync.WaitGroup{}
	defer wg.Wait()

	instances := e.config.Instances
	wg.Add(len(instances))
	for _, instance := range instances {
		instance := instance
		go func() {
			NewScraper(&instance, e, ch).Scrape()
			wg.Done()
		}()
	}
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	// RDS metrics
	for _, m := range e.metrics {
		ch <- m.Desc
	}

	// Scrape time
	ch <- scrapeTimeDesc
}
