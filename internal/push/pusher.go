package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/db"
)

type DB interface {
	FetchUnsyncedEvents(ctx context.Context, limit int) ([]db.PushEvent, error)
	MarkEventsSynced(ctx context.Context, events []db.PushEvent, pushedAt int64) error
}

type Result struct {
	BatchesSent int
	EventsSent  int
}

type Pusher struct {
	db              DB
	endpoint        string
	httpClient      *http.Client
	maxPayloadBytes int
	maxRetries      int
	baseBackoff     time.Duration
	random          *rand.Rand
}

type item struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type batch struct {
	events []db.PushEvent
	body   []byte
}

func New(db DB, endpoint string, maxPayloadBytes int) *Pusher {
	return &Pusher{
		db:              db,
		endpoint:        endpoint,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		maxPayloadBytes: maxPayloadBytes,
		maxRetries:      5,
		baseBackoff:     500 * time.Millisecond,
		random:          rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (p *Pusher) SetTestOptions(client *http.Client, retries int, backoff time.Duration) {
	if client != nil {
		p.httpClient = client
	}
	p.maxRetries = retries
	p.baseBackoff = backoff
}

func (p *Pusher) PushOnce(ctx context.Context) (Result, error) {
	if p.endpoint == "" {
		return Result{}, errors.New("push endpoint not configured")
	}

	events, err := p.db.FetchUnsyncedEvents(ctx, 5000)
	if err != nil {
		return Result{}, err
	}
	if len(events) == 0 {
		return Result{}, nil
	}

	batches, err := p.buildBatches(events)
	if err != nil {
		return Result{}, err
	}

	res := Result{}
	for _, b := range batches {
		if err := p.sendWithRetry(ctx, b.body); err != nil {
			return res, err
		}
		if err := p.db.MarkEventsSynced(ctx, b.events, time.Now().UnixMilli()); err != nil {
			return res, err
		}
		res.BatchesSent++
		res.EventsSent += len(b.events)
	}
	return res, nil
}

func (p *Pusher) buildBatches(events []db.PushEvent) ([]batch, error) {
	const baseEnvelope = len(`{"events":[]}`)
	items := make([]item, 0, len(events))
	for _, ev := range events {
		items = append(items, item{
			Type: ev.Type,
			Data: ev.Data,
		})
	}

	out := make([]batch, 0)
	curItems := make([]item, 0)
	curEvents := make([]db.PushEvent, 0)
	curSize := baseEnvelope

	flush := func() error {
		if len(curItems) == 0 {
			return nil
		}
		body, err := json.Marshal(struct {
			Events []item `json:"events"`
		}{Events: curItems})
		if err != nil {
			return err
		}
		out = append(out, batch{
			events: append([]db.PushEvent(nil), curEvents...),
			body:   body,
		})
		curItems = curItems[:0]
		curEvents = curEvents[:0]
		curSize = baseEnvelope
		return nil
	}

	for i, it := range items {
		itBytes, err := json.Marshal(it)
		if err != nil {
			return nil, err
		}
		additional := len(itBytes)
		if len(curItems) > 0 {
			additional++
		}

		if len(curItems) > 0 && curSize+additional > p.maxPayloadBytes {
			if err := flush(); err != nil {
				return nil, err
			}
		}

		curItems = append(curItems, it)
		curEvents = append(curEvents, events[i])
		curSize += additional

		// If single event is huge, still send it as its own batch.
		if len(curItems) == 1 && curSize > p.maxPayloadBytes {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Pusher) sendWithRetry(ctx context.Context, body []byte) error {
	var lastErr error
	for attempt := 0; attempt < p.maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.httpClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("push status %d", resp.StatusCode)
		}
		lastErr = err

		maxSleep := p.baseBackoff * time.Duration(1<<attempt)
		if maxSleep > 30*time.Second {
			maxSleep = 30 * time.Second
		}
		sleep := time.Duration(p.random.Int63n(int64(maxSleep) + 1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
	return fmt.Errorf("push failed after retries: %w", lastErr)
}
