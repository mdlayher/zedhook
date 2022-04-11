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
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/zedhook/internal/zedhook"
	"golang.org/x/sync/errgroup"
	"inet.af/peercred"
)

func TestServerServeUNIX(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// TODO(mdlayher): there's a bit of overlap here with the
	// unixtransport.Install API because we want to use our own Server instead
	// of httptest.Server. It's probably okay though?
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "unixtransport.sock"))
	if err != nil {
		t.Fatalf("failed to open local listener: %v", err)
	}
	defer l.Close()

	var (
		pcC = make(chan *peercred.Creds, 1)
		eg  errgroup.Group
	)

	eg.Go(func() error {
		// Serve a noop handler which fetches peercreds.
		srv := zedhook.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pcC <- zedhook.PeercredContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}),
			log.New(io.Discard, "", 0),
		)

		if err := srv.TestServe(ctx, l); err != nil {
			return fmt.Errorf("failed to serve: %v", err)
		}

		return nil
	})

	// Push a payload to the configured Server.
	c, err := zedhook.NewClient(fmt.Sprintf("http+unix://%s:/push", l.Addr()), nil)
	if err != nil {
		t.Fatalf("failed to create HTTP zedhook client: %v", err)
	}

	if err := c.Push(ctx); err != nil {
		t.Fatalf("failed to push client payload: %v", err)
	}

	cancel()
	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to wait for server: %v", err)
	}

	user, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}

	// Finally, verify the peercreds of the sender. We expect our own PID and
	// UID.
	var (
		creds  = <-pcC
		pid, _ = creds.PID()
		uid, _ = creds.UserID()
	)

	if diff := cmp.Diff(os.Getpid(), pid); diff != "" {
		t.Fatalf("unexpected peer credentials PID (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(user.Uid, uid); diff != "" {
		t.Fatalf("unexpected peer credentials UID (-want +got):\n%s", diff)
	}
}
