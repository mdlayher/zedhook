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
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// An Event is the processed version of a client Payload.
type Event struct {
	ID, EventID  int
	Timestamp    time.Time
	Class, Zpool string
}

// parseEvent processes a raw Payload into an Event.
func parseEvent(p Payload) (Event, error) {
	var (
		e Event

		// Both components of the event timestamp, processed after parsing
		// everything.
		sec, nsec int64
	)

	for _, v := range p.Variables {
		var err error
		switch v.Key {
		case "ZEVENT_CLASS":
			e.Class = v.Value
		case "ZEVENT_EID":
			e.EventID, err = strconv.Atoi(v.Value)
		case "ZEVENT_POOL":
			e.Zpool = v.Value
		case "ZEVENT_TIME_SECS":
			sec, err = strconv.ParseInt(v.Value, 10, 64)
		case "ZEVENT_TIME_NSECS":
			nsec, err = strconv.ParseInt(v.Value, 10, 64)
		}
		if err != nil {
			return Event{}, fmt.Errorf("failed to parse %q=%q: %v", v.Key, v.Value, err)
		}
	}

	e.Timestamp = time.Unix(sec, nsec)
	return e, nil
}

// scan uses the scanner to populate Event.
func (e *Event) scan(s scanner) error {
	var unix int64
	if err := s.Scan(&e.ID, &e.EventID, &unix, &e.Class, &e.Zpool); err != nil {
		return err
	}

	e.Timestamp = time.Unix(0, unix)
	return nil
}

// A zpoolStatus contains parsed output from zpool status.
type zpoolStatus struct {
	Header zpoolHeader
}

// A zpoolHeader is the header output from zpool status.
type zpoolHeader struct {
	Pool, State string
	Scan        []string
}

// A zpoolParser is a type which consumes zpool status output to produce a
// zpoolStatus structure.
type zpoolParser struct {
	s *bufio.Scanner
}

// parseZpoolStatus returns a zpoolStatus from raw zpool status output.
func parseZpoolStatus(status string) (zpoolStatus, error) {
	zp := &zpoolParser{
		// Clean up leading and trailing newlines first.
		s: bufio.NewScanner(strings.NewReader(strings.TrimSpace(status))),
	}

	zh, err := zp.header()
	if err != nil {
		return zpoolStatus{}, fmt.Errorf("failed to parse header: %v", err)
	}

	return zpoolStatus{Header: zh}, nil
}

// header produces a zpoolHeader from the zpoolParser's current location.
func (zp *zpoolParser) header() (zpoolHeader, error) {
	var zh zpoolHeader

	for zp.s.Scan() {
		// Begin scanning lines.
		//
		// TODO(mdlayher): any key can have multiple lines. Generalize.
		text := strings.TrimSpace(zp.s.Text())
		if strings.HasPrefix(text, "scan:") {
			scan, err := zp.headerScan(text)
			if err != nil {
				return zpoolHeader{}, err
			}

			zh.Scan = scan
			text = zp.s.Text()
		}

		k, v, ok := strings.Cut(text, ":")
		if !ok {
			return zpoolHeader{}, fmt.Errorf("unexpected header status line: %s", zp.s.Text())
		}

		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)

		switch k {
		case "config":
			// Start of pool topology section.
			return zh, nil
		case "pool":
			zh.Pool = v
		case "state":
			zh.State = v
		}
	}

	return zpoolHeader{}, fmt.Errorf("unexpected end of header: %v", zp.s.Err())
}

// headerScan scans header lines for the scan statement.
func (zp *zpoolParser) headerScan(text string) ([]string, error) {
	_, v, ok := strings.Cut(text, ":")
	if !ok {
		return nil, fmt.Errorf("unexpected header scan line: %s", text)
	}

	lines := []string{strings.TrimSpace(v)}

	for zp.s.Scan() {
		text := strings.TrimSpace(zp.s.Text())
		if strings.HasPrefix(text, "config:") {
			return lines, nil
		} else {
			lines = append(lines, text)
		}
	}

	return nil, fmt.Errorf("did not find termination for scan section: %v", zp.s.Err())
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
