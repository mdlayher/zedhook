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
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// An Event is the processed version of a client Payload.
type Event struct {
	// Core metadata about an Event.
	ID, EventID  int
	Timestamp    time.Time
	Class, Zpool string

	// Unprocessed variables associated with the Event.
	Variables []Variable
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
		default:
			// Unprocessed, add to the Event as-is.
			e.Variables = append(e.Variables, v)
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

var _ json.Marshaler = Event{}

// MarshalJSON returns the JSON object for an Event.
func (e Event) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonEvent{
		ID:        e.ID,
		EventID:   e.EventID,
		Timestamp: e.Timestamp.UnixNano(),
		Class:     e.Class,
		Zpool:     e.Zpool,
		Variables: e.Variables,
	})
}

// UnmarshalJSON unpacks the JSON for an Event.
func (e *Event) UnmarshalJSON(b []byte) error {
	var je jsonEvent
	if err := json.Unmarshal(b, &je); err != nil {
		return err
	}

	*e = Event{
		ID:        je.ID,
		EventID:   je.EventID,
		Timestamp: time.Unix(0, je.Timestamp),
		Class:     je.Class,
		Zpool:     je.Zpool,
		Variables: je.Variables,
	}

	return nil
}

// A jsonEvent is the JSON body for an Event.
type jsonEvent struct {
	ID        int        `json:"id"`
	EventID   int        `json:"event_id"`
	Timestamp int64      `json:"timestamp"`
	Class     string     `json:"class"`
	Zpool     string     `json:"zpool"`
	Variables []Variable `json:"variables"`
}

// A Variable is a key/value pair for an environment variable passed by ZED.
type Variable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// scan uses the scanner to populate a Variable.
func (v *Variable) scan(s scanner) error { return s.Scan(&v.Key, &v.Value) }
