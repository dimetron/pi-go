package diagram

import (
	"strings"
	"testing"
	"time"
)

// freezeNow pins the gantt clock for one test and restores it afterwards, so a
// case that depends on "now" asserts against a value it chose rather than
// against whatever day the suite happens to run on.
func freezeNow(t *testing.T, at time.Time) {
	t.Helper()
	prev := Now
	Now = func() time.Time { return at }
	t.Cleanup(func() { Now = prev })
}

// TestGanttFallsBackToNow covers the three task shapes whose start cannot be
// read from the source, so the parser reads the clock instead. Each of them
// used to be untestable: the result moved with the wall clock.
func TestGanttFallsBackToNow(t *testing.T) {
	frozen := time.Date(2017, time.July, 25, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		source string
		task   string // label of the task whose start is the clock
	}{
		{
			name:   "after references an unknown task",
			source: "gantt\n    cherry :c, after nosuchtask, 1d\n",
			task:   "cherry",
		},
		{
			name:   "start date does not parse",
			source: "gantt\n    apple :a, not-a-date, 1d\n",
			task:   "apple",
		},
		{
			// The duration carries a space so the parser cannot mistake it
			// for a task id, which is what leaves it as the only field and
			// sends the start to the clock.
			name:   "bare duration with no earlier task",
			source: "gantt\n    kiwi :2 weeks\n",
			task:   "kiwi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			freezeNow(t, frozen)

			gd := parseGantt(tt.source)
			if gd == nil {
				t.Fatal("parseGantt returned nil")
			}

			var found *ganttTask
			for i := range gd.tasks {
				if gd.tasks[i].label == tt.task {
					found = &gd.tasks[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("task %q not parsed from %q", tt.task, tt.source)
			}
			if !found.start.Equal(frozen) {
				t.Errorf("start = %v, want the frozen clock %v", found.start, frozen)
			}
		})
	}
}

// TestGanttTodayMarkerUsesFrozenClock asserts the today marker is drawn from
// the injected clock: with now inside the chart's range the marker is present,
// and with now outside it the chart renders without one.
func TestGanttTodayMarkerUsesFrozenClock(t *testing.T) {
	const source = "gantt\n    apple :a, 2017-07-20, 1w\n"

	t.Run("clock inside the range draws the marker", func(t *testing.T) {
		freezeNow(t, time.Date(2017, time.July, 23, 12, 0, 0, 0, time.UTC))
		assertCanvasContains(t, RenderGantt(source, false, nil), "today")
	})

	t.Run("clock outside the range draws no marker", func(t *testing.T) {
		freezeNow(t, time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC))
		if out := RenderGantt(source, false, nil).ToString(); strings.Contains(out, "today") {
			t.Errorf("marker drawn for a clock outside the chart range:\n%s", out)
		}
	})
}
