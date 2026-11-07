package node

import (
	"context"
	"errors"
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
	return sch.Next(after).UTC(), nil
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
			return n.sendScheduled(ctx, sc)
		}
	}
	return store.Message{}, errf("not_found", "no schedule %s", id)
}

func (n *Node) sendScheduled(ctx context.Context, sc store.Schedule) (store.Message, error) {
	m, _, err := n.Send(ctx, Principal{Kind: "user", Name: n.login}, SendRequest{To: sc.Agent, Body: sc.Body,
		Cwd: sc.Cwd, Model: sc.Model, Effort: sc.Effort, Meta: map[string]string{"schedule": sc.ID}})
	msg, errText := m.ID, ""
	if err != nil {
		errText = err.Error()
	}
	_ = n.st.RanSchedule(n.ctx, sc.ID, msg, errText)
	return m, err
}

// runSchedules sends what is due (the maintenance loop's tick).
func (n *Node) runSchedules(ctx context.Context, now time.Time) {
	list, err := n.st.Schedules(ctx)
	if err != nil {
		return
	}
	for _, sc := range list {
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
		if _, err := n.sendScheduled(ctx, sc); err != nil {
			n.log.Warn("schedule", "id", sc.ID, "err", err)
		}
	}
}
