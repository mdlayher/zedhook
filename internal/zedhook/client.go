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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/peterbourgon/unixtransport"
)

// The default addresses zedhookd will bind to, given no configuration.
const (
	defaultUNIX = "http+unix:///run/zedhookd.sock:/push"
	defaultHTTP = "http://localhost:9919/push"
)

// A Client is a client for the zedhookd server.
type Client struct {
	c     *http.Client
	addrs []string
}

// NewClient creates a Client for the zedhookd server specified by addr.
//
// If addr is empty, the default zedhookd addresses will be tried in order:
//   - http+unix:///run/zedhookd.sock:/push
//   - http://localhost:9919/push
//
// If a non-empty addr is set, that address will be used and no fallback paths
// will be attempted.
//
// If c is nil, a default HTTP client which supports "http", "https",
// "http+unix", and "https+unix" address schemes (see
// https://github.com/peterbourgon/unixtransport) will be configured.
func NewClient(addr string, c *http.Client) (*Client, error) {
	if c == nil {
		// By default, support http{,s}+unix.
		t := &http.Transport{}
		unixtransport.Register(t)

		c = &http.Client{
			Timeout:   10 * time.Second,
			Transport: t,
		}
	}

	var addrs []string
	if addr == "" {
		// Defaults.
		addrs = []string{defaultUNIX, defaultHTTP}
	} else {
		// User-defined address.
		u, err := url.Parse(addr)
		if err != nil {
			return nil, err
		}

		addrs = []string{u.String()}
	}

	return &Client{
		c:     c,
		addrs: addrs,
	}, nil
}

// contentJSON is the Content-Type header for UTF-8 JSON.
const contentJSON = "application/json; charset=utf-8"

// Push gathers a Payload from environment variables set by ZED and pushes that
// payload to an instance of zedhookd. It may also invoke ZFS shell commands as
// necessary to gather additional context.
func (c *Client) Push(ctx context.Context) error {
	p, err := envPayload(ctx)
	if err != nil {
		return fmt.Errorf("failed to gather environment payload: %v", err)
	}

	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("failed to marshal payload JSON: %v", err)
	}

	// Track the last error from Client.Do in the event we run out of addresses
	// to try contacting.
	var doErr error
	for _, addr := range c.addrs {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("failed to create HTTP POST request: %v", err)
		}
		r.Header.Set("Content-Type", contentJSON)

		res, err := c.c.Do(r)
		if err != nil {
			doErr = fmt.Errorf("failed to push data: %v", err)
			continue
		}
		_ = res.Body.Close()

		if c := res.StatusCode; c != http.StatusNoContent {
			doErr = fmt.Errorf("unexpected zedhookd response code: HTTP %d", c)
			continue
		}

		return nil
	}

	return fmt.Errorf("could not reach zedhookd using addresses %v: %v", c.addrs, doErr)
}
