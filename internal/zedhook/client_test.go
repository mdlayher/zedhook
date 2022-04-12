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

package zedhook_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/zedhook/internal/zedhook"
	"github.com/peterbourgon/unixtransport"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/slices"
)

func TestClientPush(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T, s *zedhook.Storage) (*zedhook.Client, <-chan zedhook.Payload)
	}{
		{
			name: "HTTP",
			fn:   testHTTP,
		},
		{
			name: "UNIX",
			fn:   testUNIX,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			s := zedhook.MemoryStorage()
			c, pC := tt.fn(t, s)

			// Fun fact, mdlayher argued against adding this before using it in
			// this code about 1.5 years later:
			// https://github.com/golang/go/issues/41260#issuecomment-688378662
			t.Setenv("ZEVENT_POOL", "tank")
			if err := c.Push(ctx); err != nil {
				t.Fatalf("failed to push: %v", err)
			}

			// Fixed version number.
			p := <-pC
			if diff := cmp.Diff(zedhook.V0, p.Version()); diff != "" {
				t.Fatalf("unexpected payload version (-want +got):\n%s", diff)
			}

			// Assuming a typical test execution environment, $HOME should be
			// collected and pushed to this test zedhookd instance.
			i := slices.IndexFunc(p.Variables, func(v zedhook.Variable) bool {
				return v.Key == "HOME"
			})
			if i == -1 {
				t.Fatal("HOME was not found in variables")
			}

			if diff := cmp.Diff(os.Getenv("HOME"), p.Variables[i].Value); diff != "" {
				t.Fatalf("unexpected $HOME value (-want +got):\n%s", diff)
			}

			// We're done pushing events, so verify that the server parsed our
			// payload successfully and noted the input zpool.
			events, err := s.ListEvents(ctx, 0, 1)
			if err != nil {
				t.Fatalf("failed to list events: %v", err)
			}

			want := []zedhook.Event{{
				ID:        1,
				Timestamp: time.Unix(0, 0),
				Zpool:     "tank",
			}}

			if diff := cmp.Diff(want, events); diff != "" {
				t.Fatalf("unexpected Events (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClientPushDefaultsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := zedhook.NewClient("", nil)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Assume zedhookd is not running. Try to push data to the default
	// addresses, and verify that we tried the defaults.
	err = c.Push(ctx)
	if err == nil {
		t.Skip("skipping, zedhookd appears to be running on this system")
	}

	// We control the error string, good enough.
	for _, addr := range []string{zedhook.DefaultUNIX, zedhook.DefaultHTTP} {
		if !strings.Contains(err.Error(), addr) {
			t.Fatalf("did not find address %q in error %v", addr, err)
		}
	}

	t.Logf("err: %v", err)
}

func TestClientPushErrorHTTPStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(srv.Close)

	c, err := zedhook.NewClient(srv.URL+"/push", nil)
	if err != nil {
		t.Fatalf("failed to create HTTP zedhook client: %v", err)
	}

	err = c.Push(ctx)
	if err == nil {
		t.Fatal("expected an error, but none occurred")
	}

	if !strings.Contains(err.Error(), `HTTP 404: "404 page not found"`) {
		t.Fatalf("expected HTTP 404 error, but got: %v", err)
	}

	t.Logf("err: %v", err)
}

// testHTTP creates a Client backed by a TCP HTTP server which returns its
// payload on a channel.
func testHTTP(t *testing.T, s *zedhook.Storage) (*zedhook.Client, <-chan zedhook.Payload) {
	t.Helper()

	handler, pC := testHandler(s)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := zedhook.NewClient(srv.URL+"/push", nil)
	if err != nil {
		t.Fatalf("failed to create HTTP zedhook client: %v", err)
	}

	return c, pC
}

// testUNIX creates a Client backed by a UNIX socket HTTP server which returns
// its payload on a channel.
func testUNIX(t *testing.T, s *zedhook.Storage) (*zedhook.Client, <-chan zedhook.Payload) {
	t.Helper()

	handler, pC := testHandler(s)
	srv := httptest.NewUnstartedServer(handler)
	unixtransportInstall(t, srv)

	c, err := zedhook.NewClient(srv.URL+"/push", srv.Client())
	if err != nil {
		t.Fatalf("failed to create UNIX+HTTP zedhook client: %v", err)
	}

	return c, pC
}

// testHandler creates a http.Handler and Payload channel which sends the
// contents of the first request once decoded.
func testHandler(s *zedhook.Storage) (http.Handler, <-chan zedhook.Payload) {
	pC := make(chan zedhook.Payload, 1)

	// Discard all logs, pass payload to pC.
	h := zedhook.NewHandler(s, log.New(io.Discard, "", 0), prometheus.NewPedanticRegistry())
	h.OnPayload = func(p zedhook.Payload) { pC <- p }

	return h, pC
}

// Hypothetical unixtransport.Install API, see:
// https://github.com/peterbourgon/unixtransport/pull/3

// unixtransportInstall takes an unstarted *httptest.Server and configures the
// server and associated client for HTTP over UNIX socket transport.
//
// If the server is already started, unixtransportInstall will panic.
func unixtransportInstall(tb testing.TB, s *httptest.Server) {
	tb.Helper()

	ln, err := net.Listen("unix", filepath.Join(tb.TempDir(), "unixtransport.sock"))
	if err != nil {
		tb.Errorf("unixtransport: httptest.Server not configured: %v", err)
		return
	}

	tb.Cleanup(func() {
		if err := ln.Close(); err != nil {
			tb.Errorf("unixtransport: close listener: %v", err)
		}
	})

	// Plumb in the listener and start the server using that listener. This sets
	// srv.URL, which we must later override.
	s.Listener = ln
	s.Start()

	unixtransport.Register(s.Client().Transport.(*http.Transport))

	// srv.URL is already set but it hard-codes the http scheme. Manually
	// construct a valid URL suitable for use by unixtransport-enabled HTTP(S)
	// clients.
	var scheme string
	if s.TLS != nil {
		scheme = "https+unix"
	} else {
		scheme = "http+unix"
	}

	s.URL = fmt.Sprintf("%s://%s:", scheme, ln.Addr())
}
