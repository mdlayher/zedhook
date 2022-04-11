// Copyright 2022 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zedhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mdlayher/metricslite"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"inet.af/peercred"
)

var _ http.Handler = (*Handler)(nil)

// A Handler is an http.Handler for zedhookd logic.
type Handler struct {
	// OnPayload is an optional hook which is fired when a valid zedhook payload
	// push request is sent to a Server. If not nil, the callback will be fired
	// with the contents of the Payload.
	OnPayload func(p Payload)

	s   Storage
	mux http.Handler
	ll  *log.Logger
	mm  metrics
}

// NewHandler constructs an http.Handler for use with the Server. If Storage is
// nil, no data will be persisted between zedhookd runs.
func NewHandler(s Storage, ll *log.Logger, reg *prometheus.Registry) *Handler {
	h := &Handler{
		s:  s,
		ll: ll,
		mm: newMetrics(metricslite.NewPrometheus(reg)),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/push", h.push)
	mux.HandleFunc("/events", h.events)
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog: ll,
	}))

	h.mux = mux
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "zedhook")

	if r.URL.Path == "/" {
		// TODO(mdlayher): banner.
		_, _ = io.WriteString(w, "zedhookd\n")
		return
	}

	h.mux.ServeHTTP(w, r)
}

// push implements the HTTP POST push logic for the all-zedhook ZEDLET.
func (h *Handler) push(w http.ResponseWriter, r *http.Request) {
	pr, ok := h.pushRequest(w, r)
	if !ok {
		// Middleware already wrote HTTP response.
		h.mm.PushErrorsTotal(1.0)
		return
	}

	if pr.Creds != nil {
		h.logf(r, "local: %s, creds: %+v", pr.Local, pr.Creds)
	} else {
		h.logf(r, "local: %s", pr.Local)
	}

	h.logf(r, "payload: %d variables", len(pr.Payload.Variables))

	event, err := parseEvent(pr.Payload)
	if err != nil {
		return
	}

	// TODO(mdlayher): consider combining with h.OnPayload.
	if h.s != nil {
		if err := h.s.SaveEvent(context.Background(), event); err != nil {
			h.logf(r, "failed to save client event: %v", err)
			return
		}
	}

	if h.OnPayload != nil {
		// Fire payload hook.
		h.OnPayload(pr.Payload)
	}

	h.mm.PushTotal(1, event.Zpool)
	w.WriteHeader(http.StatusNoContent)
}

// A pushRequest contains HTTP request data sent by a client to the push handler.
type pushRequest struct {
	Payload Payload
	Local   net.Addr
	// May be nil if connection did not arrive over UNIX socket.
	Creds *peercred.Creds
}

// pushRequest is a middleware which parses a valid pushRequest or returns an
// HTTP error status to the client due to an invalid request. If pushRequest
// returns true, the request is valid and can be processed.
func (h *Handler) pushRequest(w http.ResponseWriter, r *http.Request) (*pushRequest, bool) {
	// We expect client push to use one-shot requests from the ZEDLET and
	// therefore there's no advantage to keepalives.
	w.Header().Set("Connection", "close")

	if r.Method != http.MethodPost {
		return nil, h.errorf(
			w, r,
			http.StatusMethodNotAllowed,
			"method not allowed: %q", r.Method,
		)
	}

	if ct := r.Header.Get("Content-Type"); ct != contentJSON {
		return nil, h.errorf(
			w, r,
			http.StatusBadRequest,
			"bad request content type: %q", ct,
		)
	}

	var p Payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return nil, h.errorf(
			w, r,
			http.StatusBadRequest,
			"bad request payload: %v", err,
		)
	}

	var (
		// Fetch data stored in the request context. For UNIX sockets, creds
		// will be non-nil.
		ctx   = r.Context()
		local = ctx.Value(http.LocalAddrContextKey).(net.Addr)
		creds = peercredContext(ctx)
	)

	return &pushRequest{
		Payload: p,
		Local:   local,
		Creds:   creds,
	}, true
}

// events implements GET /events.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", contentJSON)

	var body responseBody
	er, err := h.eventsRequest(w, r)
	if err != nil {
		h.logf(r, "failed to send events: %v", err)
		body = errorBody(err)
	} else {
		body = okBody(er.Page, er.Events)
	}

	_ = json.NewEncoder(w).Encode(body)
}

// An eventsRequest contains HTTP request data sent by a client to fetch and
// display events from zedhookd.
type eventsRequest struct {
	Events []Event
	Page   page
}

// eventsRequest is a middleware which parses a valid eventsRequest or returns
// an HTTP error status to the client.
func (h *Handler) eventsRequest(w http.ResponseWriter, r *http.Request) (*eventsRequest, error) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return nil, fmt.Errorf("method not allowed: %q", r.Method)
	}

	p, err := queryPage(r.URL.Query())
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, fmt.Errorf("invalid pagination request parameters: %v", err)
	}

	events, err := h.s.ListEvents(r.Context(), p.Offset, p.Limit)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return nil, fmt.Errorf("failed to list events: %v", err)
	}

	return &eventsRequest{
		Events: events,
		Page:   p,
	}, nil
}

// errorf writes a formatted error to the client and to the Handler's logger.
// It always returns "nil, false" for use in h.pushRequest.
func (h *Handler) errorf(
	w http.ResponseWriter, r *http.Request,
	status int,
	format string, v ...any,
) bool {
	text := fmt.Sprintf(format, v...)

	h.logf(r, text)
	http.Error(w, text, status)
	return false
}

// logf logs a formatted log for a client request if the Handler logger is not
// nil.
func (h *Handler) logf(r *http.Request, format string, v ...any) {
	if h.ll == nil {
		return
	}

	h.ll.Printf("%s: %s", r.RemoteAddr, fmt.Sprintf(format, v...))
}

// responseBody is the top-level HTTP JSON response body object.
type responseBody struct {
	Error    *responseError    `json:"error"`
	Metadata *responseMetadata `json:"metadata,omitempty"`
	Events   []Event           `json:"events,omitempty"`
}

// responseMetadata contains metadata for the client about an HTTP response.
type responseMetadata struct {
	Page page `json:"page"`
}

// responseError contains error information for a client about an HTTP response.
type responseError struct {
	Message string `json:"message"`
}

// A page contains API pagination parameters.
type page struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

// okBody returns a responseBody with events and associated metadata.
func okBody(page page, events []Event) responseBody {
	return responseBody{
		Metadata: &responseMetadata{Page: page},
		Events:   events,
	}
}

// errorBody returns a responseBody with the input error.
func errorBody(err error) responseBody {
	return responseBody{Error: &responseError{Message: err.Error()}}
}

// queryPage creates a page from query parameters.
func queryPage(query url.Values) (page, error) {
	type tuple struct {
		Name  string
		Value string
	}

	newTuple := func(name string) tuple {
		return tuple{
			Name:  name,
			Value: query.Get(name),
		}
	}

	// By default, set a zero offset and reasonably large limit.
	p := page{Limit: 1000}
	for _, t := range [2]tuple{newTuple("offset"), newTuple("limit")} {
		if t.Value == "" {
			// Not specified.
			continue
		}

		v, err := strconv.Atoi(t.Value)
		if err != nil {
			return page{}, fmt.Errorf("failed to parse query parameter %q: %v", t.Name, err)
		}

		switch t.Name {
		case "offset":
			p.Offset = v
		case "limit":
			p.Limit = v
		default:
			panicf("unhandled query parameter: %v", t)
		}
	}

	return p, nil
}

// metrics contains metrics for the zedhookd handler.
type metrics struct {
	PushTotal, PushErrorsTotal metricslite.Counter
}

// newMetrics produces metrics based on the input metricslite.Interface.
func newMetrics(mm metricslite.Interface) metrics {
	return metrics{
		PushTotal: mm.Counter(
			"zedhook_push_total",
			"The number of times a client successfully pushed data to the zedhookd server, partitioned by ZFS pool.",
			// TODO(mdlayher): ultimately the values for this field are created
			// by user input which means we could experience a cardinality
			// explosion in the case of malicious input. The intent is for
			// all-zedhook and zedhookd to live on the same system and
			// communicate over localhost, but there's no way to know for sure
			// that a given pool actually exists unless we exec in this server,
			// which is something we'd like to avoid.
			//
			// Reconsider in the the future.
			"zpool",
		),

		PushErrorsTotal: mm.Counter(
			"zedhook_push_errors_total",
			"The number of times a client pushed invalid data to the zedhookd server.",
		),
	}
}

func panicf(format string, a ...any) {
	panic(fmt.Sprintf(format, a...))
}
