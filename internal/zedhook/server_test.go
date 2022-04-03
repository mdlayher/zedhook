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
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/zedhook/internal/zedhook"
)

func TestHandlerErrors(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		mod  func(r *http.Request)
		code int
	}{
		{
			name: "not found",
			mod:  func(r *http.Request) { r.URL.Path = "/notfound" },
			code: http.StatusNotFound,
		},
		{
			name: "method not allowed",
			mod:  func(r *http.Request) { r.Method = http.MethodGet },
			code: http.StatusMethodNotAllowed,
		},
		{
			name: "bad request Content-Type",
			mod:  func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
			code: http.StatusBadRequest,
		},
		{
			name: "bad request JSON body",
			body: []byte("xxx"),
			code: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, pC := testHandler()
			srv := httptest.NewServer(handler)
			defer srv.Close()

			// Body is empty or a slice of bytes.
			var r io.Reader
			if tt.body != nil {
				r = bytes.NewReader(tt.body)
			}

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/push", r)
			if err != nil {
				t.Fatalf("failed to create HTTP request: %v", err)
			}
			req.Header.Set("Content-Type", zedhook.ContentJSON)

			// If set, modify the valid request to make it invalid in some form.
			if tt.mod != nil {
				tt.mod(req)
			}

			res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				t.Fatalf("failed to perform HTTP request: %v", err)
			}

			// All requests should return non-204 and no payload.
			if len(pC) > 0 {
				p := <-pC
				t.Errorf("expected empty payload channel, but got: %+v", p)
			}

			if diff := cmp.Diff(tt.code, res.StatusCode); diff != "" {
				t.Errorf("unexpected HTTP status code (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff("zedhook", res.Header.Get("Server")); diff != "" {
				t.Errorf("unexpected Server HTTP header (-want +got):\n%s", diff)
			}
		})
	}
}
