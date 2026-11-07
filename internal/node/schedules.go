package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/k1dory/acpost/internal/store"
)

// Schedules: the node sends a task on a cron schedule, from its user, as a new thread each run
// (send's own checks apply: the agent, the directory, the settings). A run missed while the node
// was down is sent once when it is up again, not once per miss.

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func nextRun(spec string, after time.Time) (time.Time, error) {
	sch, err := cronParser.Parse(spec)
	if err != nil {
		return time.Time{}, err
	}
	next := sch.Next(after)
	if next.IsZero() {
		return next, errors.New("it never comes (no such date)")
	}
	return next.UTC(), nil
}

// Schedules lists the schedules.
func (n *Node) Schedules(ctx context.Context, p Principal) ([]store.Schedule, error) {
	if err := userOnly(p, "schedules"); err != nil {
		return nil, err
	}
	out, err := n.st.Schedules(ctx)
	if err != nil {
		return nil, errf("unavailable", "%v", err)
	}
	return out, nil
}

// PutSchedule adds or changes a schedule; its next run follows from its cron.
func (n *Node) PutSchedule(ctx context.Context, p Principal, sc store.Schedule) (store.Schedule, error) {
	if err := userOnly(p, "schedules"); err != nil {
		return sc, err
	}
	sc.Name, sc.Cron, sc.Agent = strings.TrimSpace(sc.Name), strings.TrimSpace(sc.Cron), strings.TrimSpace(sc.Agent)
	switch {
	case sc.Name == "" || len(sc.Name) > 120:
		return sc, errf("invalid", "a schedule's name: 1 to 120 characters")
	case strings.TrimSpace(sc.Body) == "" || len(sc.Body) > int(n.cfg.Limits.MaxBody):
		return sc, errf("invalid", "the task: 1 byte up to limits.max_body")
	}
	to, err := ParseAddress(sc.Agent)
	if err != nil || to.Kind != "agent" {
		return sc, errf("invalid", "agent %q: agent:name", sc.Agent)
	}
	sc.Agent = to.String()
	next, err := nextRun(sc.Cron, time.Now())
	if err != nil {
		return sc, errf("invalid", "cron %q: %v (5 fields, or @daily, @hourly…)", sc.Cron, err)
	}
	if sc.Cwd != "" {
		if sc.Cwd, err = checkDir(sc.Cwd); err != nil {
			return sc, err
		}
	}
	if sc.ID == "" {
		sc.ID = "sch_" + store.NewID()[4:]
	}
	sc.NextRun = next
	if err := n.st.PutSchedule(ctx, sc); err != nil {
		return sc, errf("unavailable", "%v", err)
	}
	return sc, nil
}

// DeleteSchedule removes a schedule.
func (n *Node) DeleteSchedule(ctx context.Context, p Principal, id string) error {
	if err := userOnly(p, "schedules"); err != nil {
		return err
	}
	if err := n.st.DeleteSchedule(ctx, id); errors.Is(err, store.ErrNoSchedule) {
		return errf("not_found", "%v", err)
	} else if err != nil {
		return errf("unavailable", "%v", err)
	}
	return nil
}

// RunSchedule sends a schedule's task now (its next run unchanged).
func (n *Node) RunSchedule(ctx context.Context, p Principal, id string) (store.Message, error) {
	if err := userOnly(p, "schedules"); err != nil {
		return store.Message{}, err
	}
	list, err := n.st.Schedules(ctx)
	if err != nil {
		return store.Message{}, errf("unavailable", "%v", err)
	}
	for _, sc := range list {
		if sc.ID == id {
			return n.sendScheduled(ctx, sc, time.Time{})
		}
	}
	return store.Message{}, errf("not_found", "no schedule %s", id)
}

// pendingFor: how long an occurrence that cannot be sent (the agent's node unreachable) is tried
// again before it is given up.
const pendingFor = 24 * time.Hour

// sendScheduled sends a schedule's task: an occurrence (claimed by a tick) once, under a key of its
// own, so a resend after a crash returns the first; a manual run (zero occurrence) each time. An
// occurrence that fails for a reason that may pass (unavailable, a timeout) stays pending.
func (n *Node) sendScheduled(ctx context.Context, sc store.Schedule, occurrence time.Time) (store.Message, error) {
	req := SendRequest{To: sc.Agent, Body: sc.Body, Cwd: sc.Cwd, Model: sc.Model, Effort: sc.Effort,
		Meta: map[string]string{"schedule": sc.ID}}
	if !occurrence.IsZero() {
		req.IdemKey = fmt.Sprintf("sch:%s:%d", sc.ID, occurrence.UnixMilli())
	}
	m, _, err := n.Send(ctx, Principal{Kind: "user", Name: n.login}, req)
	errText := ""
	if err != nil {
		if n.ctx.Err() != nil {
			return m, err // shutting down: the occurrence stays pending, sent when the node is up
		}
		errText = err.Error()
		if !occurrence.IsZero() && passing(ctx, err) && time.Since(occurrence) < pendingFor {
			_ = n.st.FailedSchedule(n.ctx, sc.ID, occurrence, errText)
			return m, err
		}
	}
	_ = n.st.RanSchedule(n.ctx, sc.ID, occurrence, m.ID, errText)
	return m, err
}

// passing: an error a later try may not meet. All but a malformed task: a refusal may be a full
// queue, a missing agent a node not announced again yet; tried for pendingFor, with the error shown.
func passing(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code != "invalid" && e.Code != "unsupported"
	}
	return true
}

// runSchedules claims what is due and sends it apart from the maintenance loop (a slow hub must
// not hold up expiry and recovery), one pass at a time, each send with a deadline, the pass joined
// by Close. Occurrences claimed earlier and not sent (a crash, a shutdown, the agent's node down)
// go first, if their schedule is still there and enabled.
func (n *Node) runSchedules(ctx context.Context, now time.Time) {
	if n.closed.Load() || !n.schedBusy.CompareAndSwap(false, true) {
		return
	}
	n.goLoop(func() {
		defer n.schedBusy.Store(false)
		list, err := n.st.Schedules(ctx)
		if err != nil {
			return
		}
		for _, sc := range list {
			if ctx.Err() != nil {
				return
			}
			occ := sc.Pending
			if occ.IsZero() {
				if !sc.Enabled || sc.NextRun.IsZero() || sc.NextRun.After(now) {
					continue
				}
				next, err := nextRun(sc.Cron, now)
				if err != nil {
					continue
				}
				if ok, err := n.st.TakeDue(ctx, sc.ID, sc.NextRun, next); err != nil || !ok {
					continue
				}
				occ = sc.NextRun
			}
			// As it is now: deleted or disabled while the pass ran, it is not sent.
			cur, err := n.st.Schedule(ctx, sc.ID)
			if err != nil || !cur.Enabled || !cur.Pending.Equal(occ) {
				continue
			}
			sctx, cancel := context.WithTimeout(ctx, time.Minute)
			if _, err := n.sendScheduled(sctx, cur, occ); err != nil {
				n.log.Warn("schedule", "id", sc.ID, "err", err)
			}
			cancel()
		}
	})
}
