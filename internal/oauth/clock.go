package oauth

import (
	"sync"
	"time"
)

type Stopper interface {
	Stop() bool
}

type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
	AfterFunc(time.Duration, func()) Stopper
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }

func (realClock) AfterFunc(d time.Duration, fn func()) Stopper { return time.AfterFunc(d, fn) }

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock   *fakeClock
	when    time.Time
	fn      func()
	stopped bool
	fired   bool
}

func NewFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

func (c *fakeClock) AfterFunc(d time.Duration, fn func()) Stopper {
	c.mu.Lock()
	defer c.mu.Unlock()

	timer := &fakeTimer{
		clock: c,
		when:  c.now.Add(d),
		fn:    fn,
	}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeClock) Step(d time.Duration) {
	target := c.Now().Add(d)

	for {
		timer := c.nextDueTimer(target)
		if timer == nil {
			c.mu.Lock()
			c.now = target
			c.mu.Unlock()
			return
		}

		c.mu.Lock()
		c.now = timer.when
		timer.fired = true
		timer.stopped = true
		fn := timer.fn
		c.mu.Unlock()

		if fn != nil {
			fn()
		}
	}
}

func (c *fakeClock) nextDueTimer(target time.Time) *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()

	var next *fakeTimer
	for _, timer := range c.timers {
		if timer == nil || timer.stopped || timer.fired {
			continue
		}
		if timer.when.After(target) {
			continue
		}
		if next == nil || timer.when.Before(next.when) {
			next = timer
		}
	}
	return next
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}
