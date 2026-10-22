package client

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		kind   TaskKind
		status string
		want   Outcome
	}{
		{KindDeploymentTask, "waiting", Pending},
		{KindDeploymentTask, "inprogress", Pending},
		{KindDeploymentTask, "succeeded", Succeeded},
		{KindDeploymentTask, "failed", Failed},
		{KindDeploymentTask, "in progress", Unknown},
		{KindDeploymentTask, "finished", Succeeded},

		{KindValidation, "waiting", Pending},
		{KindValidation, "in progress", Pending},
		{KindValidation, "In Progress", Pending},
		{KindValidation, "inprogress", Unknown},
		{KindValidation, "succeeded", Succeeded},
		{KindValidation, "failed", Failed},

		{KindEdgeHostnameHistory, "inprogress", Pending},
		{KindEdgeHostnameHistory, "succeeded", Succeeded},
		{KindEdgeHostnameHistory, "failed", Failed},

		{KindPurge, "inprogress", Pending},
		{KindPurge, "finished", Finished},
		{KindPurge, "succeeded", Succeeded},
		{KindPurge, "failed", Failed},
		{KindPurge, "in progress", Unknown},
		{KindPrefetch, "finished", Finished},

		{KindDeploymentTask, "", Unknown},
		{TaskKind(99), "succeeded", Unknown},
	}
	for _, c := range cases {
		if got := Classify(c.kind, c.status); got != c.want {
			t.Errorf("Classify(%s, %q) = %v, want %v", c.kind, c.status, got, c.want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(KindDeploymentTask, "inprogress") {
		t.Error("inprogress must not be terminal")
	}
	if !IsTerminal(KindDeploymentTask, "failed") {
		t.Error("failed must be terminal")
	}
	if !IsTerminal(KindPurge, "finished") {
		t.Error("finished must be terminal for purges")
	}
	if IsTerminal(KindValidation, "inprogress") {
		t.Error("validations never use inprogress; must not be terminal either")
	}
}

func TestPollStopsWhenDone(t *testing.T) {
	calls := 0
	err := Poll(context.Background(), PollConfig{Interval: time.Millisecond, Timeout: time.Second}, func(context.Context) (bool, error) {
		calls++
		return calls == 3, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestPollReturnsCheckError(t *testing.T) {
	boom := errors.New("boom")
	err := Poll(context.Background(), PollConfig{Interval: time.Millisecond, Timeout: time.Second}, func(context.Context) (bool, error) {
		return false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestPollTimesOut(t *testing.T) {
	err := Poll(context.Background(), PollConfig{Interval: time.Millisecond, Timeout: 20 * time.Millisecond}, func(context.Context) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrPollTimeout) {
		t.Fatalf("err = %v, want ErrPollTimeout", err)
	}
}

func TestPollHonoursContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	err := Poll(ctx, PollConfig{Interval: time.Millisecond, Timeout: time.Minute}, func(context.Context) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
