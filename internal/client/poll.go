package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TaskKind selects the terminal-state vocabulary for one family of async
// API objects. The API is not uniform, so each kind has its own table.
type TaskKind int

const (
	// KindDeploymentTask: waiting -> inprogress -> succeeded | failed.
	KindDeploymentTask TaskKind = iota
	// KindValidation: waiting -> "in progress" (with a space) -> succeeded | failed.
	KindValidation
	// KindEdgeHostnameHistory: same vocabulary as deployment tasks, read from
	// history[0].status on the edge hostname itself.
	KindEdgeHostnameHistory
	// KindPurge and KindPrefetch: waiting -> inprogress -> finished. There is no
	// failed state; success is judged separately from successRate.
	KindPurge
	KindPrefetch
)

func (k TaskKind) String() string {
	switch k {
	case KindDeploymentTask:
		return "deployment task"
	case KindValidation:
		return "validation"
	case KindEdgeHostnameHistory:
		return "edge hostname operation"
	case KindPurge:
		return "purge"
	case KindPrefetch:
		return "prefetch"
	default:
		return fmt.Sprintf("task kind %d", int(k))
	}
}

// Outcome classifies a status string.
type Outcome int

const (
	// Pending means keep polling.
	Pending Outcome = iota
	// Succeeded is a terminal success.
	Succeeded
	// Failed is a terminal failure.
	Failed
	// Finished is the purge/prefetch terminal state; success must be read from
	// successRate by the caller.
	Finished
	// Unknown is a status string outside the kind's vocabulary.
	Unknown
)

var vocabularies = map[TaskKind]map[string]Outcome{
	KindDeploymentTask: {
		"waiting":    Pending,
		"inprogress": Pending,
		"succeeded":  Succeeded,
		// The API's shared task status enum also carries "finished"; for a
		// deployment task it can only mean the work is over.
		"finished": Succeeded,
		"failed":   Failed,
	},
	KindValidation: {
		"waiting":     Pending,
		"in progress": Pending,
		"succeeded":   Succeeded,
		"failed":      Failed,
	},
	KindEdgeHostnameHistory: {
		"waiting":    Pending,
		"inprogress": Pending,
		"succeeded":  Succeeded,
		"failed":     Failed,
	},
	// Purges and prefetches normally end in "finished" with the outcome in
	// successRate, but they share the API's task status enum, so the other two
	// terminal values are accepted as well.
	KindPurge: {
		"waiting":    Pending,
		"inprogress": Pending,
		"finished":   Finished,
		"succeeded":  Succeeded,
		"failed":     Failed,
	},
	KindPrefetch: {
		"waiting":    Pending,
		"inprogress": Pending,
		"finished":   Finished,
		"succeeded":  Succeeded,
		"failed":     Failed,
	},
}

// Classify maps a raw status string onto an Outcome for the given kind.
// Matching is case-insensitive and ignores surrounding whitespace, but it does
// not normalise internal spaces: "in progress" is only valid for validations
// and "inprogress" is never valid for them.
func Classify(kind TaskKind, status string) Outcome {
	vocab, ok := vocabularies[kind]
	if !ok {
		return Unknown
	}
	if o, ok := vocab[strings.ToLower(strings.TrimSpace(status))]; ok {
		return o
	}
	return Unknown
}

// IsTerminal reports whether status ends polling for the given kind.
func IsTerminal(kind TaskKind, status string) bool {
	switch Classify(kind, status) {
	case Succeeded, Failed, Finished:
		return true
	default:
		return false
	}
}

// PollConfig is the cadence for one endpoint group. Both values are provider
// configurable; the defaults come from the PRD and should be tuned against the
// real API.
type PollConfig struct {
	Interval time.Duration
	Timeout  time.Duration
}

// Default cadences per endpoint group.
var (
	DefaultValidationPoll   = PollConfig{Interval: 10 * time.Second, Timeout: 10 * time.Minute}
	DefaultDeploymentPoll   = PollConfig{Interval: 20 * time.Second, Timeout: 10 * time.Minute}
	DefaultEdgeHostnamePoll = PollConfig{Interval: 20 * time.Second, Timeout: 10 * time.Minute}
	DefaultPurgePoll        = PollConfig{Interval: 30 * time.Second, Timeout: 30 * time.Minute}
)

// ErrPollTimeout is wrapped in the error returned when a poll exceeds its Timeout.
var ErrPollTimeout = errors.New("timed out waiting for task to finish")

// Poll calls check at a fixed interval (no backoff) until it reports done, it
// returns an error, ctx is cancelled, or cfg.Timeout elapses. The first check
// runs immediately. A zero Interval defaults to one second, a zero Timeout to
// ten minutes.
func Poll(ctx context.Context, cfg PollConfig, check func(ctx context.Context) (done bool, err error)) error {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		done, err := check(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%w after %s", ErrPollTimeout, cfg.Timeout)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
