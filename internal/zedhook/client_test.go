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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/zedhook/internal/zedhook"
	"github.com/peterbourgon/unixtransport"
	"golang.org/x/exp/slices"
)

func TestClientPush(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T) (*zedhook.Client, <-chan zedhook.Payload)
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
			c, pC := tt.fn(t)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

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

// testHTTP creates a Client backed by a TCP HTTP server which returns its
// payload on a channel.
func testHTTP(t *testing.T) (*zedhook.Client, <-chan zedhook.Payload) {
	t.Helper()

	handler, pC := testHandler()
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
func testUNIX(t *testing.T) (*zedhook.Client, <-chan zedhook.Payload) {
	t.Helper()

	handler, pC := testHandler()
	srv := unixtransport.NewTestServer(t, handler)
	t.Cleanup(srv.Close)

	c, err := zedhook.NewClient(srv.URL+"/push", srv.Client())
	if err != nil {
		t.Fatalf("failed to create UNIX+HTTP zedhook client: %v", err)
	}

	return c, pC
}

// testHandler creates a http.Handler and Payload channel which sends the
// contents of the first request once decoded.
func testHandler() (http.Handler, <-chan zedhook.Payload) {
	pC := make(chan zedhook.Payload, 1)

	// Discard all logs, pass payload to pC.
	h := zedhook.NewHandler(log.New(io.Discard, "", 0))
	h.OnPayload = func(p zedhook.Payload) { pC <- p }

	return h, pC
}

func panicf(format string, a ...any) {
	panic(fmt.Sprintf(format, a...))
}
