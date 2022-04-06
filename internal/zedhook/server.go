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
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mdlayher/metricslite"
	"github.com/mdlayher/netx/multinet"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
	"inet.af/peercred"
)

// A Server is the zedhookd server entry point.
type Server struct {
	srv *http.Server
	ll  *log.Logger
}

// NewServer constructs a Server which serves traffic using the input Handler.
func NewServer(handler http.Handler, ll *log.Logger) *Server {
	return &Server{
		srv: &http.Server{
			Handler:      handler,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			ErrorLog:     ll,
			ConnContext:  peercredConnContext,
		},
		ll: ll,
	}
}

// peercredConnContext is a http.Server ConnContext hook which attaches UNIX
// socket peer credentials to a request's context.
//
// TODO(mdlayher): follow up on https://github.com/inetaf/peercred/issues/9.
func peercredConnContext(ctx context.Context, c net.Conn) context.Context {
	// Best effort; connection may be UNIX or TCP.
	if creds, err := peercred.Get(c); err == nil {
		ctx = context.WithValue(ctx, keyCreds, creds)
	}

	return ctx
}

// peercredContext fetches *peercred.Creds from an HTTP request context. The
// Creds may be nil.
func peercredContext(ctx context.Context) *peercred.Creds {
	creds, _ := ctx.Value(keyCreds).(*peercred.Creds)
	return creds
}

// A contextKey is an opaque structure used as a key for context.Context values.
type contextKey struct{ name string }

// keyCreds stores *peercred.Creds in a context.Context Value.
var keyCreds = &contextKey{"peercred"}

// Serve serves the zedhookd receiver and blocks until the context is canceled.
func (s *Server) Serve(ctx context.Context) error {
	// TODO(mdlayher): make configurable, default to UNIX and HTTP.
	var ls []net.Listener

	tcpL, err := net.Listen("tcp", "localhost:9919")
	if err != nil {
		return fmt.Errorf("failed to listen TCP: %v", err)
	}
	ls = append(ls, tcpL)

	unixL, err := net.ListenUnix("unix", &net.UnixAddr{
		Name: "/run/zedhookd/zedhookd.sock",
	})
	switch {
	case err == nil:
		// OK, continue setup.
		ls = append(ls, unixL)
		unixL.SetUnlinkOnClose(true)
	case errors.Is(err, os.ErrNotExist):
		// Parent directory doesn't exist.
		s.logf("skipping UNIX listener setup: %v", err)
	default:
		// Terminal error.
		return fmt.Errorf("failed to listen UNIX: %v", err)
	}

	// Combine the listeners and serve connections on both at once.
	return s.serve(ctx, multinet.Listen(ls...))
}

// serve uses the net.Listener to serve the zedhook receiver, blocking until the
// context is canceled. The Server will close the net.Listener on context
// cancelation.
func (s *Server) serve(ctx context.Context, l net.Listener) error {
	defer l.Close()

	// Listeners are ready, use Serve's context as a base.
	s.srv.BaseContext = func(_ net.Listener) context.Context { return ctx }

	s.logf("started server, listeners: %v", l.Addr())

	var eg errgroup.Group
	eg.Go(func() error {
		if err := s.srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("failed to serve: %v", err)
		}

		return nil
	})

	<-ctx.Done()
	s.logf("server signaled, shutting down")

	// We received a signal. This context is detached from parent because the
	// parent is already canceled but we want to give a short period of time for
	// outstanding requests to complete and drain.
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()

	if err := s.srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %v", err)
	}

	// Also cleans up the UNIX socket file. Ignore errors relating to the
	// listener already being closed by Shutdown above.
	if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("failed to close HTTP listener: %v", err)
	}

	return nil
}

// logf logs a formatted log if the Server logger is not nil.
func (s *Server) logf(format string, v ...any) {
	if s.ll == nil {
		return
	}

	s.ll.Printf(format, v...)
}

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
		return h.errorf(
			w, r,
			http.StatusMethodNotAllowed,
			"method not allowed: %q", r.Method,
		)
	}

	if ct := r.Header.Get("Content-Type"); ct != contentJSON {
		return h.errorf(
			w, r,
			http.StatusBadRequest,
			"bad request content type: %q", ct,
		)
	}

	var p Payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return h.errorf(
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

// errorf writes a formatted error to the client and to the Handler's logger.
// It always returns "nil, false" for use in h.pushRequest.
func (h *Handler) errorf(
	w http.ResponseWriter, r *http.Request,
	status int,
	format string, v ...any,
) (*pushRequest, bool) {
	text := fmt.Sprintf(format, v...)

	h.logf(r, text)
	http.Error(w, text, status)
	return nil, false
}

// logf logs a formatted log for a client request if the Handler logger is not
// nil.
func (h *Handler) logf(r *http.Request, format string, v ...any) {
	if h.ll == nil {
		return
	}

	h.ll.Printf("%s: %s", r.RemoteAddr, fmt.Sprintf(format, v...))
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
