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
