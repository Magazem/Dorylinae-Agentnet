package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// R55-037: when Stats fails, /metrics answers 500 with no metric values a
// scraper could record as real zeros.
func TestMetricsStatsFailureIs500WithoutValues(t *testing.T) {
	rs := relay.New(relay.Options{})
	rs.Close() // the queue is closed: Stats now fails
	rec := httptest.NewRecorder()
	metricsHandler(rs).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "relay_queue_rows") {
		t.Fatalf("body carries zero values after a Stats failure:\n%s", rec.Body.String())
	}
}

// R55-190: the operator gauge for the database size is served.
func TestMetricsDiskGauges(t *testing.T) {
	rs := relay.New(relay.Options{})
	defer rs.Close()
	rec := httptest.NewRecorder()
	metricsHandler(rs).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "relay_db_bytes 0") {
		t.Fatalf("status %d body:\n%s", rec.Code, rec.Body.String())
	}
}
