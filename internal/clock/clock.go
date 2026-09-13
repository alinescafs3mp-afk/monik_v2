package clock

import "time"

// Clock is injectable so tests can accelerate duration logic without
// replacing native soak or boot evidence.
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
	Sleep(d time.Duration)
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.t.C }
func (t realTicker) Stop()               { t.t.Stop() }

type Real struct{}

func (Real) Now() time.Time                         { return time.Now().UTC() }
func (Real) Since(t time.Time) time.Duration        { return time.Since(t) }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (Real) NewTicker(d time.Duration) Ticker       { return realTicker{time.NewTicker(d)} }
func (Real) Sleep(d time.Duration)                  { time.Sleep(d) }

// Fake is a controllable clock for unit tests.
type Fake struct {
	now    time.Time
	slept  time.Duration
	timers []fakeTimer
}

type fakeTimer struct {
	when time.Time
	ch   chan time.Time
}

func NewFake(t time.Time) *Fake {
	return &Fake{now: t.UTC()}
}

func (f *Fake) Now() time.Time { return f.now }

func (f *Fake) Since(t time.Time) time.Duration { return f.now.Sub(t) }

func (f *Fake) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.timers = append(f.timers, fakeTimer{when: f.now.Add(d), ch: ch})
	return ch
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	ch := make(chan time.Time, 1)
	return &fakeTicker{ch: ch, d: d, f: f}
}

func (f *Fake) Sleep(d time.Duration) {
	f.Advance(d)
	f.slept += d
}

func (f *Fake) Advance(d time.Duration) {
	f.now = f.now.Add(d)
	remaining := f.timers[:0]
	for _, t := range f.timers {
		if !f.now.Before(t.when) {
			select {
			case t.ch <- f.now:
			default:
			}
		} else {
			remaining = append(remaining, t)
		}
	}
	f.timers = remaining
}

type fakeTicker struct {
	ch chan time.Time
	d  time.Duration
	f  *Fake
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               {}
