package main

import (
	"fmt"
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
	"github.com/percona/rds_exporter/factory"
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
	// NOTE: This handler was retained for backward compatibility. See: https://jira.percona.com/browse/PMM-1901.
	{
		basicCollector := basic.New(cfg, sess, true)

		registry := prometheus.NewRegistry()
		registry.MustRegister(prometheus.NewProcessCollector(os.Getpid(), "")) // from prometheus.DefaultGatherer
		registry.MustRegister(prometheus.NewGoCollector())                     // from prometheus.DefaultGatherer

		registry.MustRegister(basicCollector)
		registry.MustRegister(client)
		http.Handle(*basicMetricsPathF, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
			ErrorLog:      log.NewErrorLogger(),
			ErrorHandling: promhttp.ContinueOnError,
		}))
	}

	// This collector should be only one for both cases, because it creates goroutines which sends API requests to Amazon in background.
	enhancedCollector := enhanced.NewCollector(sess)

	// enhanced metrics
	// NOTE: This handler was retained for backward compatibility. See: https://jira.percona.com/browse/PMM-1901.
	{
		registry := prometheus.NewRegistry()
		registry.MustRegister(enhancedCollector)
		http.Handle(*enhancedMetricsPathF, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
			ErrorLog:      log.NewErrorLogger(),
			ErrorHandling: promhttp.ContinueOnError,
		}))
	}

	// all metrics (with filtering)
	{
		f := factory.New(cfg, sess, map[string]prometheus.Collector{"enhanced": enhancedCollector, "client": client})
		handler := newHandler(f)
		http.Handle("/metrics", handler)
	}

	log.Infof("Metrics: http://%s%s", *listenAddressF, "/metrics")
	log.Fatal(http.ListenAndServe(*listenAddressF, nil))
}

// handler wraps an unfiltered http.Handler but uses a filtered handler,
// created on the fly, if filtering is requested. Create instances with
// newHandler. It used for collectors filtering.
type handler struct {
	unfilteredHandler http.Handler
	factory           *factory.Collectors
}

func newHandler(factory *factory.Collectors) *handler {
	h := &handler{factory: factory}
	if innerHandler, err := h.innerHandler(); err != nil {
		log.Fatalf("Couldn't create metrics handler: %s", err)
	} else {
		h.unfilteredHandler = innerHandler
	}
	return h
}

// ServeHTTP implements http.Handler.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	filters := r.URL.Query()["collect[]"]
	log.Debugln("collect query:", filters)

	if len(filters) == 0 {
		// No filters, use the prepared unfiltered handler.
		h.unfilteredHandler.ServeHTTP(w, r)
		return
	}

	filteredHandler, err := h.innerHandler(filters...)
	if err != nil {
		log.Warnln("Couldn't create filtered metrics handler:", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf("Couldn't create filtered metrics handler: %s", err)))
		return
	}
	filteredHandler.ServeHTTP(w, r)
}

func (h *handler) innerHandler(filters ...string) (http.Handler, error) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewProcessCollector(os.Getpid(), "")) // from prometheus.DefaultGatherer
	registry.MustRegister(prometheus.NewGoCollector())                     // from prometheus.DefaultGatherer

	collectors := h.factory.Create(filters)

	// register all collectors by default.
	if len(filters) == 0 {
		for name, c := range collectors {
			if err := registry.Register(c); err != nil {
				return nil, err
			}
			log.Infof("Collector '%s' was registered", name)
		}
	}

	// register only filtered collectors.
	for _, name := range filters {
		if c, ok := collectors[name]; ok {
			if err := registry.Register(c); err != nil {
				return nil, err
			}
			log.Infof("Collector '%s' was registered", name)
		}
	}

	handler := promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{
			ErrorLog:      log.NewErrorLogger(),
			ErrorHandling: promhttp.ContinueOnError,
		},
	)

	return handler, nil
}
