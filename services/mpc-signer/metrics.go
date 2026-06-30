package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metrics.go defines the Prometheus metrics exported by the MPC signer at
// /metrics. The api-gateway has its own metrics; these cover the signing layer
// specifically so we can alert on MPC error rate and latency independently.
var (
	signTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mpc_sign_total",
		Help: "Total MPC sign operations by result",
	}, []string{"result"}) // result: success | failed | invalid

	signDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "mpc_sign_duration_seconds",
		Help:    "MPC signing duration",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	})

	auditWriteTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mpc_audit_write_total",
		Help: "Total immudb audit writes by result",
	}, []string{"result"}) // result: success | failed | skipped
)
