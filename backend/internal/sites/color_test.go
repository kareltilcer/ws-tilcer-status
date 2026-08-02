package sites

import "testing"

func TestComputeColor(t *testing.T) {
	const threshold = 2
	cases := []struct {
		name string
		in   ColorInputs
		want Color
	}{
		{
			name: "monitored reachable no crashes -> green",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 0, RedThreshold: threshold, HasAnyCheck: true},
			want: Green,
		},
		{
			name: "monitored reachable with open crash -> orange",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 0, RedThreshold: threshold, RecentOpenCrashes: 1, HasAnyCheck: true},
			want: Orange,
		},
		{
			name: "monitored never checked -> unknown",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 0, RedThreshold: threshold, HasAnyCheck: false},
			want: Unknown,
		},
		{
			name: "two consecutive failures -> red",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 2, RedThreshold: threshold, HasAnyCheck: true, PriorColor: Green},
			want: Red,
		},
		{
			name: "single failure holds prior green (debounce)",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 1, RedThreshold: threshold, HasAnyCheck: true, PriorColor: Green},
			want: Green,
		},
		{
			name: "single failure holds prior unknown (first-ever check fails)",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 1, RedThreshold: threshold, HasAnyCheck: true, PriorColor: Unknown},
			want: Unknown,
		},
		{
			name: "single failure after crash aged out -> green (not stale orange)",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 1, RedThreshold: threshold, RecentOpenCrashes: 0, HasAnyCheck: true, PriorColor: Orange},
			want: Green,
		},
		{
			name: "recovery: streak reset to 0 after failures -> green",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 0, RedThreshold: threshold, HasAnyCheck: true, PriorColor: Red},
			want: Green,
		},
		{
			name: "red takes precedence over crashes",
			in:   ColorInputs{MonitorEnabled: true, FailStreak: 3, RedThreshold: threshold, RecentOpenCrashes: 5, HasAnyCheck: true},
			want: Red,
		},
		{
			name: "crash-only site with crashes -> orange",
			in:   ColorInputs{MonitorEnabled: false, RecentOpenCrashes: 2, RedThreshold: threshold},
			want: Orange,
		},
		{
			name: "crash-only site no crashes -> green (never unknown)",
			in:   ColorInputs{MonitorEnabled: false, RedThreshold: threshold},
			want: Green,
		},
		{
			name: "crash-only site never goes red regardless of streak",
			in:   ColorInputs{MonitorEnabled: false, FailStreak: 99, RedThreshold: threshold},
			want: Green,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ComputeColor(c.in); got != c.want {
				t.Fatalf("ComputeColor(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
