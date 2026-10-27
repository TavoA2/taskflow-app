package store

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolCollector exports pgxpool statistics at scrape time. They are the USE signals of the
// database connection pool: utilization (acquired vs max) and saturation (acquires that waited).
type PoolCollector struct {
	pool *pgxpool.Pool

	acquired, idle, total, max  *prometheus.Desc
	acquires, emptyAcquires     *prometheus.Desc
	acquireSeconds, waitSeconds *prometheus.Desc
}

func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc("taskflow_db_pool_"+name, help, nil, nil)
	}
	return &PoolCollector{
		pool:           pool,
		acquired:       desc("acquired_conns", "Connections currently in use."),
		idle:           desc("idle_conns", "Idle connections in the pool."),
		total:          desc("total_conns", "Total connections (acquired, idle and being constructed)."),
		max:            desc("max_conns", "Maximum pool size."),
		acquires:       desc("acquires_total", "Successful connection acquisitions."),
		emptyAcquires:  desc("empty_acquires_total", "Acquisitions that had to wait because no connection was idle (saturation)."),
		acquireSeconds: desc("acquire_duration_seconds_total", "Total time spent acquiring connections."),
		waitSeconds:    desc("empty_acquire_wait_seconds_total", "Total time spent waiting for a connection when none was idle."),
	}
}

func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.acquires, c.emptyAcquires, c.acquireSeconds, c.waitSeconds} {
		ch <- d
	}
}

func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) { ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v) }
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}

	gauge(c.acquired, float64(s.AcquiredConns()))
	gauge(c.idle, float64(s.IdleConns()))
	gauge(c.total, float64(s.TotalConns()))
	gauge(c.max, float64(s.MaxConns()))
	counter(c.acquires, float64(s.AcquireCount()))
	counter(c.emptyAcquires, float64(s.EmptyAcquireCount()))
	counter(c.acquireSeconds, s.AcquireDuration().Seconds())
	counter(c.waitSeconds, s.EmptyAcquireWaitTime().Seconds())
}
