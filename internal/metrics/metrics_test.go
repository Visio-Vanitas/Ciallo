package metrics

import (
	"strings"
	"testing"
)

func TestRecorderWritesPrometheusMetrics(t *testing.T) {
	rec := New()
	rec.IncActiveConnections()
	rec.RecordStatus("route a", "backend:25565", "hit", false)
	rec.RecordLogin("route a", "backend:25565", "ip_banned")
	rec.RecordBackendDialError("route a", "backend:25565")
	rec.RecordFail2BanBlock("route a", "ip")

	var out strings.Builder
	if err := rec.WritePrometheus(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"ciallo_active_connections 1",
		`ciallo_status_requests_total{route="route_a",backend="backend:25565",cache_result="hit",fallback_used="false"} 1`,
		`ciallo_login_requests_total{route="route_a",backend="backend:25565",fail2ban_action="ip_banned"} 1`,
		`ciallo_backend_dial_errors_total{route="route_a",backend="backend:25565"} 1`,
		`ciallo_fail2ban_blocks_total{route="route_a",kind="ip"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics missing %q:\n%s", want, text)
		}
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	got := sanitizeLabelValue("route with spaces and \"quotes\"")
	if got != "route_with_spaces_and_quotes_" {
		t.Fatalf("sanitize = %q", got)
	}
	if got := sanitizeLabelValue(""); got != "unknown" {
		t.Fatalf("empty sanitize = %q", got)
	}
}
