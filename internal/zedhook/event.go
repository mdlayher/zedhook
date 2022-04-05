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
	"fmt"
	"strconv"
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