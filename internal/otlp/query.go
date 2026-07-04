package otlp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Clients bundles the read-side HTTP endpoints served by oteldb, addressed via port-forward.
// Each base is a host:port authority (no scheme); http:// is assumed.
type Clients struct {
	HTTP      *http.Client
	TempoBase string // Tempo/TraceQL API (default port 3200)
	LokiBase  string // Loki/LogQL API (default port 3100)
	PromBase  string // Prometheus/PromQL API (default port 9090)
}

// NewClients builds query clients for the given port-forwarded authorities.
func NewClients(tempo, loki, prom string) *Clients {
	return &Clients{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		TempoBase: tempo,
		LokiBase:  loki,
		PromBase:  prom,
	}
}

func (c *Clients) getJSON(ctx context.Context, rawURL string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("GET %s -> %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %w (body: %s)", rawURL, err, string(body))
		}
	}
	return resp.StatusCode, nil
}

// --- Tempo (traces) ---

// TraceExists reports whether a trace with the given hex ID is retrievable via the Tempo API.
// The Tempo TraceByID response is protobuf-only, so we rely on the HTTP status rather than
// decoding the body: 200 => found, 404 => not (yet) present.
func (c *Clients) TraceExists(ctx context.Context, traceID string) (bool, error) {
	u := fmt.Sprintf("http://%s/api/traces/%s", c.TempoBase, url.PathEscape(traceID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/protobuf") // Accept is a required header for this route.
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("GET %s -> %d", u, resp.StatusCode)
	}
}

// --- Loki (logs) ---

type lokiQueryResponse struct {
	Data struct {
		Result []struct {
			Values [][2]string `json:"values"` // [ [ "<ns ts>", "<line>" ], ... ]
		} `json:"result"`
	} `json:"data"`
}

// LogsContain runs a LogQL range query over the last hour and reports whether any returned line
// contains substr.
func (c *Clients) LogsContain(ctx context.Context, logQL, substr string) (bool, error) {
	now := time.Now()
	q := url.Values{}
	q.Set("query", logQL)
	q.Set("start", strconv.FormatInt(now.Add(-time.Hour).UnixNano(), 10))
	q.Set("end", strconv.FormatInt(now.Add(time.Minute).UnixNano(), 10))
	q.Set("limit", "1000")
	u := fmt.Sprintf("http://%s/loki/api/v1/query_range?%s", c.LokiBase, q.Encode())

	var lr lokiQueryResponse
	if _, err := c.getJSON(ctx, u, &lr); err != nil {
		return false, err
	}
	for _, stream := range lr.Data.Result {
		for _, v := range stream.Values {
			if strings.Contains(v[1], substr) {
				return true, nil
			}
		}
	}
	return false, nil
}

// --- Prometheus (metrics) ---

type promQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	} `json:"data"`
}

// SeriesExists runs an instant PromQL query and reports whether it returned any series.
func (c *Clients) SeriesExists(ctx context.Context, promQL string) (bool, error) {
	q := url.Values{}
	q.Set("query", promQL)
	u := fmt.Sprintf("http://%s/api/v1/query?%s", c.PromBase, q.Encode())

	var pr promQueryResponse
	if _, err := c.getJSON(ctx, u, &pr); err != nil {
		return false, err
	}
	if pr.Status != "success" {
		return false, fmt.Errorf("promQL %q returned status %q", promQL, pr.Status)
	}
	return len(pr.Data.Result) > 0, nil
}
