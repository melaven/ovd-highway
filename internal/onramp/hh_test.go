package onramp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ovd/internal/domain"
)

func TestHHConnectorPoll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("HH-User-Agent"); got != hhUserAgent {
			t.Errorf("HH-User-Agent = %q, want %q", got, hhUserAgent)
		}
		if got := r.Header.Get("User-Agent"); got != hhUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, hhUserAgent)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want %q", got, "application/json")
		}
		_, _ = w.Write([]byte(`{"items":[{"name":"Go developer","alternate_url":"https://hh.ru/vacancy/1","snippet":{"requirement":"Requires <highlighttext>Go</highlighttext>","responsibility":"Build a service"},"salary":{"from":100000,"to":150000,"currency":"RUR"}}]}`))
	}))
	defer server.Close()

	connector := NewHHConnector()
	connector.client = server.Client()
	connector.endpoint = server.URL

	signals := make(chan domain.RawSignal, 1)
	if err := connector.poll(context.Background(), signals); err != nil {
		t.Fatalf("poll: %v", err)
	}

	signal := <-signals
	if signal.Source != "hh" || signal.JobType != "vacancy" {
		t.Errorf("signal metadata = %+v", signal)
	}
	if signal.Text != "Go developer\nRequires Go\nBuild a service" {
		t.Errorf("signal text = %q", signal.Text)
	}
	if signal.Budget != "100000 - 150000 RUR" {
		t.Errorf("signal budget = %q", signal.Budget)
	}
}

func TestFormatHHSalary(t *testing.T) {
	from, to := 100000, 150000
	tests := []struct {
		name   string
		salary *hhSalary
		want   string
	}{
		{name: "range", salary: &hhSalary{From: &from, To: &to, Currency: "RUR"}, want: "100000 - 150000 RUR"},
		{name: "no salary", salary: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatHHSalary(tt.salary); got != tt.want {
				t.Errorf("formatHHSalary() = %q, want %q", got, tt.want)
			}
		})
	}
}
