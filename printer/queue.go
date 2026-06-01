// Persistent job queue with single-worker BLE serialisation.
// BLE access is inherently single-threaded for this printer; serialising via
// one goroutine means we never have to think about concurrent connect/write.
//
// The worker either reconnects on demand per job (default) or holds the
// connection open and pings periodically to defeat the printer's idle sleep
// timer (KeepAliveInterval > 0).
package printer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/synestry/catprint/jobs"
	"github.com/synestry/catprint/printer/ble"
)

// Renderer turns markdown into a 384-px-wide grayscale bitmap.
type Renderer func(content string) (*image.Gray, error)

// Connector returns a connected Printer. Injectable for tests.
type Connector func(ctx context.Context, addr string) (*Printer, error)

// Config controls queue worker behaviour.
type Config struct {
	Store             *jobs.Store
	Render            Renderer
	Connect           Connector
	PrinterAddr       string
	PollInterval      time.Duration
	SweepInterval     time.Duration
	MaxRetries        int
	RetryBackoff      time.Duration
	ConnectTimeout    time.Duration
	FeedAfter         int
	KeepAliveInterval time.Duration // 0 disables; >0 holds connection open and pings
}

func (c *Config) withDefaults() {
	if c.PollInterval == 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.SweepInterval == 0 {
		c.SweepInterval = 60 * time.Second
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	}
	if c.RetryBackoff == 0 {
		c.RetryBackoff = 2 * time.Second
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 30 * time.Second
	}
	if c.Connect == nil {
		c.Connect = Connect
	}
	if c.FeedAfter == 0 {
		c.FeedAfter = 4
	}
}

// Queue is the running worker.
type Queue struct {
	cfg    Config
	wake   chan struct{}
	stopMu sync.Mutex
	stopCh chan struct{}
	doneCh chan struct{}

	addrMu sync.Mutex
	addr   string
	online bool // last connect attempt succeeded

	// Subscribers waiting for specific job completion. Keyed by job ID.
	waitMu sync.Mutex
	waits  map[string][]chan struct{}
}

func NewQueue(cfg Config) (*Queue, error) {
	if cfg.Store == nil {
		return nil, errors.New("queue: Store is required")
	}
	if cfg.Render == nil {
		return nil, errors.New("queue: Render is required")
	}
	cfg.withDefaults()
	return &Queue{
		cfg:    cfg,
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		addr:   cfg.PrinterAddr,
		waits:  map[string][]chan struct{}{},
	}, nil
}

// Start launches the worker goroutine.
func (q *Queue) Start(ctx context.Context) { go q.run(ctx) }

// Stop signals the worker to exit and waits for it.
func (q *Queue) Stop() {
	q.stopMu.Lock()
	defer q.stopMu.Unlock()
	select {
	case <-q.stopCh:
		return
	default:
		close(q.stopCh)
	}
	<-q.doneCh
}

// Notify pokes the worker.
func (q *Queue) Notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Submit enqueues markdown and pokes the worker.
func (q *Queue) Submit(source, content string) (*jobs.Job, error) {
	j, err := q.cfg.Store.Enqueue(source, content)
	if err != nil {
		return nil, err
	}
	q.Notify()
	return j, nil
}

// SubmitAndWait submits a job and blocks until it leaves the queued state
// (sent / failed / expired) or ctx is cancelled. Returns the final job state.
func (q *Queue) SubmitAndWait(ctx context.Context, source, content string) (*jobs.Job, error) {
	j, err := q.Submit(source, content)
	if err != nil {
		return nil, err
	}
	done := q.registerWait(j.ID)
	defer q.cancelWait(j.ID, done)

	select {
	case <-done:
	case <-ctx.Done():
		return q.cfg.Store.Get(j.ID)
	}
	return q.cfg.Store.Get(j.ID)
}

func (q *Queue) registerWait(id string) chan struct{} {
	ch := make(chan struct{})
	q.waitMu.Lock()
	q.waits[id] = append(q.waits[id], ch)
	q.waitMu.Unlock()
	return ch
}

func (q *Queue) cancelWait(id string, ch chan struct{}) {
	q.waitMu.Lock()
	defer q.waitMu.Unlock()
	subs := q.waits[id]
	for i, c := range subs {
		if c == ch {
			q.waits[id] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(q.waits[id]) == 0 {
		delete(q.waits, id)
	}
}

func (q *Queue) notifyDone(id string) {
	q.waitMu.Lock()
	subs := q.waits[id]
	delete(q.waits, id)
	q.waitMu.Unlock()
	for _, c := range subs {
		close(c)
	}
}

// Addr returns the current printer MAC (may be "" before discovery).
func (q *Queue) Addr() string {
	q.addrMu.Lock()
	defer q.addrMu.Unlock()
	return q.addr
}

func (q *Queue) setAddr(a string) {
	q.addrMu.Lock()
	q.addr = a
	q.addrMu.Unlock()
}

// Online reports whether the last connect attempt succeeded. Unlike Addr,
// which only says a MAC is known, this reflects real reachability.
func (q *Queue) Online() bool {
	q.addrMu.Lock()
	defer q.addrMu.Unlock()
	return q.online
}

func (q *Queue) setOnline(v bool) {
	q.addrMu.Lock()
	q.online = v
	q.addrMu.Unlock()
}

func (q *Queue) run(ctx context.Context) {
	defer close(q.doneCh)

	// tinygo bluetooth on Windows uses WinRT/COM, which must run all calls on
	// the same OS thread (goroutines normally hop threads). Pin this worker so
	// every BLE op — connect, write, ping — stays on one thread for its life.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	pollT := time.NewTicker(q.cfg.PollInterval)
	defer pollT.Stop()
	sweepT := time.NewTicker(q.cfg.SweepInterval)
	defer sweepT.Stop()

	// Conn held across ticks when KeepAliveInterval > 0.
	var p *Printer
	defer func() {
		if p != nil {
			_ = p.Close()
		}
	}()

	keepaliveC := make(<-chan time.Time)
	var keepaliveT *time.Ticker
	if q.cfg.KeepAliveInterval > 0 {
		keepaliveT = time.NewTicker(q.cfg.KeepAliveInterval)
		defer keepaliveT.Stop()
		keepaliveC = keepaliveT.C
	}

	drain := func() {
		p = q.drainOnce(ctx, p)
		if p != nil && q.cfg.KeepAliveInterval == 0 {
			_ = p.Close()
			p = nil
		}
	}

	drain()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-pollT.C:
			drain()
		case <-q.wake:
			drain()
		case <-sweepT.C:
			if n, err := q.cfg.Store.SweepExpired(); err != nil {
				log.Printf("queue: sweep error: %v", err)
			} else if n > 0 {
				log.Printf("queue: expired %d job(s)", n)
			}
		case <-keepaliveC:
			if p != nil {
				if err := p.Ping(ctx); err != nil {
					log.Printf("queue: keepalive ping failed: %v — dropping conn", err)
					_ = p.Close()
					p = nil
					q.setOnline(false)
				}
			}
			// Self-heal: if we have no connection (ping just failed, a print
			// dropped it, or the printer was off), reconnect now so the printer
			// stays awake instead of waiting for the next job. Best-effort —
			// if the printer is unreachable this fails quietly and retries next tick.
			if p == nil {
				conn, err := q.ensureConnected(ctx)
				if err != nil {
					log.Printf("queue: keepalive reconnect failed: %v — retry next tick", err)
				} else {
					log.Printf("queue: keepalive reconnected")
					p = conn
				}
			}
		}
	}
}

// drainOnce processes all queued jobs using (and possibly opening) p.
// Returns the connection — caller decides whether to close it.
func (q *Queue) drainOnce(ctx context.Context, p *Printer) *Printer {
	for {
		if ctx.Err() != nil {
			return p
		}
		j, err := q.cfg.Store.NextQueued()
		if err != nil {
			log.Printf("queue: NextQueued: %v", err)
			return p
		}
		if j == nil {
			return p
		}

		if p == nil {
			conn, err := q.ensureConnected(ctx)
			if err != nil {
				// Printer unreachable — not a bad job. Leave it queued (don't
				// touch the retry budget) so it prints when the printer comes
				// back; the hourly sweep expires it if it never does.
				log.Printf("queue: printer unreachable, job %s stays queued: %v", j.ID, err)
				time.Sleep(q.cfg.RetryBackoff)
				return p
			}
			p = conn
		}

		if err := q.printOne(ctx, p, j); err != nil {
			retries, _ := q.cfg.Store.BumpRetry(j.ID, err.Error())
			log.Printf("queue: print failed (job=%s try=%d): %v", j.ID, retries, err)
			if retries >= q.cfg.MaxRetries {
				_ = q.cfg.Store.MarkFailed(j.ID, err.Error())
				q.notifyDone(j.ID)
			}
			_ = p.Close()
			p = nil
			time.Sleep(q.cfg.RetryBackoff)
			continue
		}
		_ = q.cfg.Store.MarkSent(j.ID)
		q.notifyDone(j.ID)
	}
}

func (q *Queue) printOne(ctx context.Context, p *Printer, j *jobs.Job) error {
	img, err := q.cfg.Render(j.Content)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if err := p.PrintBitmap(ctx, img); err != nil {
		return fmt.Errorf("print: %w", err)
	}
	if q.cfg.FeedAfter > 0 {
		if err := p.Feed(ctx, q.cfg.FeedAfter); err != nil {
			return fmt.Errorf("feed: %w", err)
		}
	}
	return nil
}

func (q *Queue) ensureConnected(ctx context.Context) (*Printer, error) {
	p, err := q.connect(ctx)
	q.setOnline(err == nil)
	return p, err
}

func (q *Queue) connect(ctx context.Context) (*Printer, error) {
	addr := q.Addr()
	if addr == "" {
		dctx, cancel := context.WithTimeout(ctx, q.cfg.ConnectTimeout)
		defer cancel()
		a, err := Discover(dctx)
		if err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}
		addr = a
		q.setAddr(addr)
	}
	cctx, cancel := context.WithTimeout(ctx, q.cfg.ConnectTimeout)
	defer cancel()
	p, err := q.cfg.Connect(cctx, addr)
	if err == nil {
		return p, nil
	}
	// BlueZ (Linux) can't connect to a MAC it hasn't seen advertise — the
	// device object isn't in its cache after a cold start. A short scan
	// populates the cache; then the connect succeeds. Harmless on Windows.
	log.Printf("queue: connect failed (%v); warming cache with a scan", err)
	warmCache(ctx, q.cfg.ConnectTimeout)
	cctx2, cancel2 := context.WithTimeout(ctx, q.cfg.ConnectTimeout)
	defer cancel2()
	return q.cfg.Connect(cctx2, addr)
}

// warmCache runs a brief discovery scan purely so the OS bluetooth stack
// learns about nearby devices before we connect by MAC. Result is ignored.
func warmCache(ctx context.Context, timeout time.Duration) {
	d := 8 * time.Second
	if timeout > 0 && timeout < d {
		d = timeout
	}
	_, _ = ble.Scan(d)
}
