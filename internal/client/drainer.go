package client

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
)

// Drainer replays queued writes to the daemon on an interval. It's started as a
// background goroutine by the MCP session; each eligible item is replayed and,
// on success, deleted. Failures bump the item's backoff and flip connectivity
// to offline so the write tools can render a queued-notice.
type Drainer struct {
	q        *wqueue.Queue
	client   *Client
	conn     *Connectivity
	interval time.Duration
	batch    int
	logger   *slog.Logger
}

// NewDrainer wires a drainer. interval<=0 defaults to 30s.
func NewDrainer(q *wqueue.Queue, c *Client, conn *Connectivity, interval time.Duration, logger *slog.Logger) *Drainer {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Drainer{q: q, client: c, conn: conn, interval: interval, batch: 50, logger: logger}
}

// Run drains on a ticker until ctx is cancelled. It drains once immediately so
// items queued by a prior session flush on startup.
func (d *Drainer) Run(ctx context.Context) {
	if n, err := d.DrainOnce(ctx); err == nil && n > 0 {
		d.logger.Info("phantom-skills: drained queued writes on startup", slog.Int("count", n))
	}
	t := time.NewTicker(d.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := d.DrainOnce(ctx); err != nil {
				d.logger.Warn("phantom-skills: drain error", slog.String("err", err.Error()))
			}
		}
	}
}

// DrainOnce replays every currently-eligible item once. Returns the number
// successfully drained. It stops early on the first failure — if the daemon is
// down, the rest are almost certainly down too, so hammering them just burns
// backoff; they stay eligible for the next tick.
func (d *Drainer) DrainOnce(ctx context.Context) (int, error) {
	items, err := d.q.NextEligible(ctx, time.Now(), d.batch)
	if err != nil {
		return 0, err
	}
	drained := 0
	for _, it := range items {
		now := time.Now()
		if rErr := d.replay(ctx, it); rErr != nil {
			_ = d.q.MarkAttempt(ctx, it.ID, now, rErr)
			d.conn.NoteFailure(now, rErr)
			return drained, nil
		}
		_ = d.q.Delete(ctx, it.ID)
		d.conn.NoteSuccess(now)
		drained++
	}
	return drained, nil
}

func (d *Drainer) replay(ctx context.Context, it *wqueue.Item) error {
	switch it.Kind {
	case wqueue.KindUsage:
		return d.client.PostUsage(ctx, it.Payload)
	case wqueue.KindProposal:
		_, err := d.client.PostProposal(ctx, it.Payload)
		return err
	default:
		return fmt.Errorf("drainer: unknown kind %q", it.Kind)
	}
}
