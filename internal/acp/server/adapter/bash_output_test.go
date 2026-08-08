package adapter

import (
	"context"
	"errors"
	"testing"
)

func TestOnBashOutputDropsEmptyContentAndNilUpdater(t *testing.T) {
	up := &fakeUpdater{}
	if err := New(up).OnBashOutput(context.Background(), "bg_1", "heartbeat", ""); err != nil {
		t.Fatalf("empty output: %v", err)
	}
	if got := len(up.updates); got != 0 {
		t.Fatalf("empty output produced %d updates, want 0", got)
	}
	if err := New(nil).OnBashOutput(context.Background(), "bg_1", "output", "hello"); err != nil {
		t.Fatalf("nil updater: %v", err)
	}
}

func TestOnBashOutputWrapsUpdaterError(t *testing.T) {
	want := errors.New("updater failed")
	up := &fakeUpdater{err: want}
	err := New(up).OnBashOutput(context.Background(), "bg_1", "stderr", "oops")
	if !errors.Is(err, want) {
		t.Fatalf("OnBashOutput error = %v, want wrapping %v", err, want)
	}
}
