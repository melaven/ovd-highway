package lane

import "testing"

func TestPassesPreFilter(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "empty", text: "", want: false},
		{name: "whitespace", text: " \n\t ", want: false},
		{name: "too short", text: "Need a developer", want: false},
		{name: "blacklisted term", text: "We need a WordPress developer for this project.", want: false},
		{name: "low budget", text: "Build a website for 3000 руб.", want: false},
		{name: "valid request", text: "We need an experienced Go engineer to build a data integration service with PostgreSQL.", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PassesPreFilter(tt.text); got != tt.want {
				t.Errorf("PassesPreFilter(%q) = %t, want %t", tt.text, got, tt.want)
			}
		})
	}
}
