package factory

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/percona/rds_exporter/basic"
	"github.com/percona/rds_exporter/config"
	"github.com/percona/rds_exporter/sessions"
)

// Collectors uses for creating collectors on fly.
type Collectors struct {
	config     *config.Config
	sessions   *sessions.Sessions
	predefined map[string]prometheus.Collector
}

// New creates collectors factory.
func New(cfg *config.Config, sess *sessions.Sessions, predefined map[string]prometheus.Collector) *Collectors {
	return &Collectors{
		config:     cfg,
		sessions:   sess,
		predefined: predefined,
	}
}

// Create creates collectors map based on filters list.
func (f *Collectors) Create(filters []string) map[string]prometheus.Collector {
	c := make(map[string]prometheus.Collector)

	// When we have no filters, all collectors will be enabled, so create "basic" one without overlapping metrics.
	if len(filters) == 0 {
		c["basic"] = basic.New(f.config, f.sessions, basic.DisableOverlapping)
	}
	// When we have only 1 filter and this is basic one, we need it with all metrics.
	if len(filters) == 1 && filterIn(filters, "basic") {
		c["basic"] = basic.New(f.config, f.sessions, basic.EnableOverlapping)
		return c
	}
	// When we have more than 1 filters and have basic one...
	if len(filters) > 1 && filterIn(filters, "basic") {
		if filterIn(filters, "enhanced") {
			c["basic"] = basic.New(f.config, f.sessions, basic.DisableOverlapping)
		} else {
			c["basic"] = basic.New(f.config, f.sessions, basic.EnableOverlapping)
		}
	}
	// Just adding all predefined collectors in map.
	for name, collector := range f.predefined {
		c[name] = collector
	}

	return c
}

func filterIn(slice []string, filter string) bool {
	for _, v := range slice {
		if v == filter {
			return true
		}
	}
	return false
}
