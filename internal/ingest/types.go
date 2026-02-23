package ingest

import "time"

const (
	QueueCapacity = 512
	MaxBatchSize  = 50
	FlushWindow   = 500 * time.Millisecond
)

type EventKind string

const (
	EventKindTrace  EventKind = "trace"
	EventKindError  EventKind = "error"
	EventKindMetric EventKind = "metric"
)

type TracePayload struct {
	Provider         string
	Model            string
	InputText        string
	OutputText       string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
	LatencyMS        int
	Status           string
	ErrorType        string
	Metadata         string
}

type ErrorPayload struct {
	ErrorType  string
	Message    string
	StackTrace string
	Severity   string
	Metadata   string
}

type MetricPayload struct {
	CPUPct        float64
	MemRSSBytes   int64
	MemAvailable  int64
	MemTotal      int64
	DiskUsedBytes int64
	DiskTotal     int64
	DiskFreeBytes int64
	Metadata      string
}

type Event struct {
	Kind      EventKind
	CreatedAt int64
	Trace     *TracePayload
	Error     *ErrorPayload
	Metric    *MetricPayload
}

func TryEnqueue(ch chan Event, event Event) bool {
	select {
	case ch <- event:
		return true
	default:
		return false
	}
}
