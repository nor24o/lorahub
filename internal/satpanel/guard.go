package satpanel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Change is a network or tunnel setting being applied. After applying, the
// panel pings the hub through the tunnel; if it does not answer in time the
// previous setting is restored, so a typo cannot cut the satellite off.
type Change struct {
	ID       int        `json:"id"`
	What     string     `json:"what"`
	State    string     `json:"state"` // applying, checking, kept, reverted, failed
	Message  string     `json:"message,omitempty"`
	Started  time.Time  `json:"started"`
	Deadline *time.Time `json:"deadline,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
}

func (c *Change) done() bool {
	return c.State == "kept" || c.State == "reverted" || c.State == "failed"
}

var errBusy = errors.New("another change is still being checked; wait for it to finish")

func (s *Server) currentChange() *Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.change == nil {
		return nil
	}
	c := *s.change
	return &c
}

func (s *Server) startChange(what string, keep bool, apply, revert func(context.Context) error) (Change, error) {
	s.mu.Lock()
	if s.change != nil && !s.change.done() {
		s.mu.Unlock()
		return Change{}, errBusy
	}
	s.seq++
	c := &Change{ID: s.seq, What: what, State: "applying", Started: s.now().UTC()}
	s.change = c
	snap := *c
	s.mu.Unlock()
	s.log.Info("network change", "what", what, "keep", keep)
	go s.runChange(c, keep, apply, revert)
	return snap, nil
}

func (s *Server) setChange(c *Change, f func(*Change)) {
	s.mu.Lock()
	f(c)
	s.mu.Unlock()
}

func (s *Server) finish(c *Change, state, msg string) {
	t := s.now().UTC()
	s.setChange(c, func(c *Change) { c.State, c.Message, c.Finished, c.Deadline = state, msg, &t, nil })
	s.log.Info("network change finished", "what", c.What, "state", state, "message", msg)
}

func (s *Server) runChange(c *Change, keep bool, apply, revert func(context.Context) error) {
	time.Sleep(s.opt.ApplyDelay) // the HTTP reply leaves before the network moves
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := apply(ctx); err != nil {
		msg := "Not applied: " + err.Error() + "."
		if rerr := revert(ctx); rerr != nil {
			msg += " Restoring the previous setting also failed: " + rerr.Error() + "."
		} else {
			msg += " The previous setting is back."
		}
		s.finish(c, "failed", msg)
		return
	}
	deadline := s.now().Add(s.opt.CheckFor).UTC()
	s.setChange(c, func(c *Change) { c.State, c.Deadline = "checking", &deadline })
	for {
		if ok, _ := s.hubPing(ctx, true); ok {
			label, _ := s.checkTarget(ctx)
			s.finish(c, "kept", "Applied. "+capital(label)+" answers.")
			return
		}
		if !s.now().Before(deadline) {
			break
		}
		time.Sleep(s.opt.CheckEvery)
	}
	secs := int(s.opt.CheckFor / time.Second)
	label, ip := s.checkTarget(ctx)
	who := label
	if ip != "" {
		who += " at " + ip
	}
	if keep {
		s.finish(c, "kept", fmt.Sprintf("Applied, but %s did not answer within %d s. Kept, as you asked.", who, secs))
		return
	}
	if err := revert(ctx); err != nil {
		s.finish(c, "failed", fmt.Sprintf("%s did not answer within %d s and restoring the previous setting failed: %s.", capital(who), secs, err))
		return
	}
	s.finish(c, "reverted", fmt.Sprintf("%s did not answer within %d s, so the previous setting was restored.", capital(who), secs))
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
