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
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mdlayher/netx/multinet"
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
