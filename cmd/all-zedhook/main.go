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

// Command all-zedhook is a ZEDLET (ZFS Event Daemon Linkage for Executable
// Tasks) which consumes all zevents produced by ZED and sends them to a
// zedhookd server.
//
// For more information on ZED and ZEDLETs, see:
// https://manpages.debian.org/unstable/zfs-zed/zed.8.en.html.
//
// Because this program is short-running and is executed by ZED every time a ZFS
// event occurs (due to the all- prefix), we try to keep package main as simple
// as possible both configuration-wise and in code complexity. The purpose of
// this program is to report as much useful raw information as possible to
// zedhookd, so that zedhookd can then decide what exactly should be done with
// that data.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/mdlayher/zedhook/internal/zedhook"
)

func main() {
	ctx, scancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer scancel()

	ctx, tcancel := context.WithTimeout(ctx, 10*time.Second)
	defer tcancel()

	// Try to connect to zedhookd using the default addresses. This assumes that
	// zedhookd will be running on the same machine and listening on one or both
	// of a UNIX socket or localhost HTTP server.
	//
	// TODO(mdlayher): plumb in optional environment variables to ZED
	// configuration to pass here?
	c, err := zedhook.NewClient("", nil)
	if err != nil {
		log.Fatalf("failed to create zedhook client: %v", err)
	}

	if err := c.Push(ctx); err != nil {
		log.Fatalf("failed to push data to zedhookd: %v", err)
	}
}
