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
	"os"
	"os/exec"
	"strings"

	"golang.org/x/exp/slices"
)

// A Version is the version number for a Payload produced by a Client.
//enumcheck:exhaustive
type Version int

// V0 is the initial, unstable version for zedhook Payload values.
const V0 Version = iota

var _ interface {
	json.Marshaler
	json.Unmarshaler
} = (*Payload)(nil)

// A Payload is the top-level zedhook payload container.
type Payload struct {
	// Environment variable key/value pairs set by ZED and gathered by a Client.
	Variables []Variable

	// Optional data gathered by executing the zpool command.
	Zpool *ZpoolPayload

	version Version
}

// A ZpoolPayload contains information from executing a zpool command.
type ZpoolPayload struct {
	// The raw output of 'zpool status tank'.
	RawStatus string `json:"raw_status"`
}

// A Variable is a key/value pair for an environment variable passed by ZED.
type Variable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Version returns the Version value embedded in this Payload.
func (p Payload) Version() Version { return p.version }

// A rawPayload is the raw JSON structure used to encode Payload values.
type rawPayload struct {
	Version   Version       `json:"version"`
	Variables []Variable    `json:"variables"`
	Zpool     *ZpoolPayload `json:"zpool"`
}

// MarshalJSON implements json.Marshaler.
func (p Payload) MarshalJSON() ([]byte, error) {
	return json.Marshal(rawPayload{
		// Send a fixed version number.
		Version: V0,

		Variables: p.Variables,
		Zpool:     p.Zpool,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *Payload) UnmarshalJSON(b []byte) error {
	var raw rawPayload
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	*p = Payload{
		Variables: raw.Variables,
		Zpool:     raw.Zpool,
		version:   raw.Version,
	}

	return nil
}

// envPayload produces a Payload from the environment variables passed into this
// command. This function assumes ZED is invoking this script and will produce
// noisy data if invoked manually.
func envPayload(ctx context.Context) (Payload, error) {
	return makePayload(ctx, os.Environ(), execZpoolStatus)
}

// An execFunc is a testable function which mimics exec.Command.CombinedOutput
// for 'zpool status pool'.
type execFunc func(ctx context.Context, zpool, pool string) ([]byte, error)

// execZpoolStatus is an execFunc which uses exec.Command.
func execZpoolStatus(ctx context.Context, zpool, pool string) ([]byte, error) {
	return exec.CommandContext(
		ctx,
		zpool,
		"status",
		// Provide very detailed pool status.
		"-ipstvP",
		pool,
	).CombinedOutput()
}

// makePayload is a testable function which produces a Payload from a set of
// environment variables and a status function.
func makePayload(ctx context.Context, envs []string, status execFunc) (Payload, error) {
	var (
		// The output payload to send to zedhookd.
		p Payload

		// Parse the zpool command path and name of the pool from the event. If
		// an event triggers which merits calling zpool status on the pool, we
		// track that here as well.
		zpool struct {
			cmd, pool string
			status    bool
		}
	)

	for _, env := range envs {
		k, v, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}

		switch k {
		case "ZPOOL":
			// Keep for later use, but don't add a variable.
			zpool.cmd = v
			continue
		case "ZEVENT_POOL":
			// Keep for later use, but also add a variable.
			zpool.pool = v
		case "ZEVENT_CLASS":
			// Only issue zpool status in response to certain events. It's
			// wasteful and unnecessary for many zpool events.
			//
			// TODO(mdlayher): read the documentation and figure out which make
			// sense to add.
			zpool.status = false ||
				v == "sysevent.fs.zfs.resilver_finish" ||
				v == "sysevent.fs.zfs.scrub_finish"
		case "",
			// Variables ZED sends that we want to ignore.
			//
			// System environment variables.
			"IFS", "PATH",
			// Binary paths we don't use.
			"ZDB", "ZED", "ZFS", "ZINJECT",
			// Unnecessary keys.
			"ZED_ZEDLET_DIR",
			// Data which is duplicated in other fields.
			"ZEVENT_TIME", "ZEVENT_TIME_STRING", "ZFS_ALIAS":
			continue
		}

		p.Variables = append(p.Variables, Variable{Key: k, Value: v})
	}

	// For deterministic output.
	slices.SortFunc(p.Variables, func(a, b Variable) bool { return a.Key < b.Key })

	if zpool.cmd != "" && zpool.pool != "" && zpool.status {
		// Annotate the payload with zpool status for applicable events.
		out, err := status(ctx, zpool.cmd, zpool.pool)
		if err != nil {
			return Payload{}, fmt.Errorf("failed to exec: zpool status %s: %v", zpool.pool, err)
		}

		p.Zpool = &ZpoolPayload{RawStatus: string(out)}
	}

	return p, nil
}
