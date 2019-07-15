package main

import (
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/percona/rds_exporter/basic"
	"github.com/percona/rds_exporter/client"
	"github.com/percona/rds_exporter/config"
	"github.com/percona/rds_exporter/enhanced"
	"github.com/percona/rds_exporter/sessions"
)

//nolint:lll
var (
	listenAddressF       = kingpin.Flag("web.listen-address", "Address on which to expose metrics and web interface.").Default(":9042").String()
	basicMetricsPathF    = kingpin.Flag("web.basic-telemetry-path", "Path under which to expose exporter's basic metrics.").Default("/basic").String()
	enhancedMetricsPathF = kingpin.Flag("web.enhanced-telemetry-path", "Path under which to expose exporter's enhanced metrics.").Default("/enhanced").String()
	configFileF          = kingpin.Flag("config.file", "Path to configuration file.").Default("config.yml").String()
	logTraceF            = kingpin.Flag("log.trace", "Enable verbose tracing of AWS requests (will log credentials).").Default("false").Bool()
)

func main() {
	log.AddFlags(kingpin.CommandLine)
	log.Infoln("Starting RDS exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())
	kingpin.Parse()

	cfg, err := config.Load(*configFileF)
	if err != nil {
		log.Fatalf("Can't read configuration file: %s", err)
	}

	client := client.New()
	sess, err := sessions.New(cfg.Instances, client.HTTP(), *logTraceF)
	if err != nil {
		log.Fatalf("Can't create sessions: %s", err)
	}

	// basic metrics + client metrics + exporter own metrics (ProcessCollector and GoCollector)
	{
		// Separate instance of basic collector for backward compatibility.  See: https://jira.percona.com/browse/PMM-1901.
		basicCollector := basic.New(cfg, sess)
		registry := prometheus.NewRegistry()
		registry.MustRegister(prometheus.NewProcessCollector(os.Getpid(), ""))
		registry.MustRegister(prometheus.NewGoCollector())
		registry.MustRegister(basicCollector)
		registry.MustRegister(client)
		http.Handle(*basicMetricsPathF, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
			ErrorLog:      log.NewErrorLogger(),
			ErrorHandling: promhttp.ContinueOnError,
		}))
	}

	// This collector should be only one for both cases.
	// It creates goroutines which sends API requests to Amazon in background.
	enhancedCollector := enhanced.NewCollector(sess)

	// enhanced metrics
	{
		registry := prometheus.NewRegistry()
		registry.MustRegister(enhancedCollector)
		http.Handle(*enhancedMetricsPathF, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
			ErrorLog:      log.NewErrorLogger(),
			ErrorHandling: promhttp.ContinueOnError,
		}))
	}

	// all metrics
	{
		// Create separate instance of basic collector and remove metrics which cross with enhanced collector.
		// Made for backward compatibility. See: https://jira.percona.com/browse/PMM-1901.
		basicCollector := basic.New(cfg, sess)
		basicCollector.Exclude("CPUUtilization", "FreeStorageSpace", "FreeableMemory")

		prometheus.MustRegister(client)
		prometheus.MustRegister(basicCollector)
		prometheus.MustRegister(enhancedCollector)
		http.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
			ErrorLog:      log.NewErrorLogger(),
			ErrorHandling: promhttp.ContinueOnError,
		}))
	}

	log.Infof("Basic metrics   : http://%s%s", *listenAddressF, *basicMetricsPathF)
	log.Infof("Enhanced metrics: http://%s%s", *listenAddressF, *enhancedMetricsPathF)
	log.Infof("All metrics: http://%s%s", *listenAddressF, "/metrics")
	log.Fatal(http.ListenAndServe(*listenAddressF, nil))
}
