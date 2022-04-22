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
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// NewStorage creates a sqlite-backed persistent storage using the input DSN.
func NewStorage(ctx context.Context, dsn string) (*Storage, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	s := &Storage{
		db: db,
		semC: func() chan struct{} {
			// Prepare the semaphore.
			c := make(chan struct{}, 1)
			c <- struct{}{}
			return c
		}(),
	}

	if err := s.setup(ctx); err != nil {
		return nil, fmt.Errorf("failed to create sqlite schema: %v", err)
	}

	return s, nil
}

// MemoryStorage creates ephemeral sqlite-backed in-memory storage.
func MemoryStorage() *Storage {
	// We don't take a caller context to avoid panicking if they cancel it.
	s, err := NewStorage(context.Background(), ":memory:")
	if err != nil {
		// There's no reason this should fail assuming fixed schema setup
		// queries.
		panicf("failed to open in-memory storage: %v", err)
	}

	return s
}

// Storage is persistent storage backed by sqlite3.
type Storage struct {
	db   *sql.DB
	semC chan struct{}
}

// Close implements Storage.
func (s *Storage) Close() error { return s.db.Close() }

// ListEventsOptions provides arguments for the Storage.ListEvents method.
type ListEventsOptions struct {
	Zpool, Class  string
	Offset, Limit int
}

// query produces the query string and arguments from o.
func (o ListEventsOptions) query() (string, []any) {
	// Construct a query based on the options set.
	//
	// TODO(mdlayher): if this becomes much more convoluted, it may be smart to
	// bring in a query builder package.
	var (
		query = `--
SELECT
	id, event_id, timestamp, class, zpool
FROM events`
		args []any
	)

	switch {
	case o.Zpool != "" && o.Class != "":
		query += "\nWHERE zpool = ? AND class = ?"
		args = append(args, o.Zpool, o.Class)
	case o.Zpool != "":
		query += "\nWHERE zpool = ?"
		args = append(args, o.Zpool)
	case o.Class != "":
		query += "\nWHERE class = ?"
		args = append(args, o.Class)
	}

	query += "\nLIMIT ?, ?;"
	args = append(args, o.Offset, o.Limit)
	return query, args
}

// ListEvents lists Events from the database given a set of options.
func (s *Storage) ListEvents(ctx context.Context, o ListEventsOptions) ([]Event, error) {
	query, args := o.query()
	return queryList(ctx, s, (*Event).scan, query, args...)
}

// GetEvent gets an Event and its associated data by ID from the database.
func (s *Storage) GetEvent(ctx context.Context, id int) (Event, error) {
	const (
		eventByID = `--
SELECT
	id, event_id, timestamp, class, zpool
FROM events
WHERE id = ?
LIMIT 1;`

		statusByID = `--
SELECT
	s.id, s.status
FROM events e
JOIN status s
ON e.id = s.event_id
WHERE e.id = ?;`

		variablesByID = `--
SELECT
	v.key, v.value
FROM events e
JOIN variables v
ON
	e.id = v.zedhook_event_id
	AND e.event_id = v.zfs_event_id
WHERE e.id = ?;`
	)

	var e Event
	err := s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := e.scan(tx.QueryRowContext(ctx, eventByID, id)); err != nil {
			// Wrap: may return sql.ErrNoRows.
			return fmt.Errorf("failed to get event: %w", err)
		}

		// Now gather associated data if present.
		var st Status
		err := st.scan(tx.QueryRowContext(ctx, statusByID, id))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if st.ID != 0 {
			e.Status = &st
		}

		vars, err := queryListTx(
			ctx,
			tx,
			(*Variable).scan,
			variablesByID,
			e.ID,
		)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		e.Variables = vars
		return nil
	})
	if err != nil {
		return Event{}, err
	}

	return e, nil
}

// SaveEvent saves an Event in the database.
func (s *Storage) SaveEvent(ctx context.Context, e Event) error {
	const (
		saveEvent = `--
INSERT INTO events (
	event_id, timestamp, class, zpool
) VALUES (?, ?, ?, ?);`

		saveStatus = `--
INSERT INTO status (
	event_id, status
) VALUES (?, ?);`

		saveVariables = `--
INSERT INTO variables (
	zedhook_event_id, zfs_event_id, key, value
) VALUES (?, ?, ?, ?);`
	)

	// We ran into SQLITE_BUSY issues before, so acquire and release a semaphore
	// to gate individual writes.
	//
	// TODO(mdlayher): this resolves busy locking but ultimately we should
	// consider moving to a batching/flushing model within a longer running
	// transaction. Investigate.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.semC:
		// Acquired, release on return.
		defer func() { s.semC <- struct{}{} }()
	}

	return s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// First save the Event.
		res, err := tx.ExecContext(
			ctx,
			saveEvent,
			e.EventID,
			e.Timestamp.UnixNano(),
			e.Class,
			e.Zpool,
		)
		if err != nil {
			return err
		}

		// Then retrieve the Event's ID, which is then used to store associated
		// data for this event.
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		e.ID = int(id)

		if e.Status != nil {
			_, err := tx.ExecContext(
				ctx,
				saveStatus,
				e.ID,
				e.Status.Status,
			)
			if err != nil {
				return err
			}
		}

		for _, v := range e.Variables {
			_, err := tx.ExecContext(
				ctx,
				saveVariables,
				e.ID,
				e.EventID,
				v.Key,
				v.Value,
			)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// setup creates the schema for Storage.
func (s *Storage) setup(ctx context.Context) error {
	const (
		createEvents = `--
CREATE TABLE IF NOT EXISTS events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id  INTEGER NOT NULL,
	timestamp INTEGER NOT NULL,
	class     TEXT NOT NULL,
	zpool     TEXT NOT NULL
);`

		createStatus = `--
CREATE TABLE IF NOT EXISTS status (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id INTEGER NOT NULL,
	status   BLOB NOT NULL,
	FOREIGN KEY(event_id) REFERENCES events(id)
);`

		createVariables = `--
CREATE TABLE IF NOT EXISTS variables (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	zedhook_event_id INTEGER NOT NULL,
	zfs_event_id     INTEGER NOT NULL,
	key              TEXT NOT NULL,
	value            TEXT NOT NULL,
	UNIQUE(id, zedhook_event_id),
	FOREIGN KEY(zedhook_event_id, zfs_event_id) REFERENCES events(id, event_id)
);`
	)

	return s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{
			createEvents,
			createStatus,
			createVariables,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}

		return nil
	})
}

// withTx begins a transaction and executes it within fn, either committing or
// rolling back the transaction depending on the return value of fn.
func (s *Storage) withTx(
	ctx context.Context,
	fn func(ctx context.Context, tx *sql.Tx) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %v", err)
	}

	if err := fn(ctx, tx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Return as-is with no wrapping, and don't bother doing a rollback.
			return err
		}

		if rerr := tx.Rollback(); err != nil {
			return fmt.Errorf("failed to rollback transaction: %v, err: %v", rerr, err)
		}

		return fmt.Errorf("failed to perform transaction: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %v", err)
	}

	return nil
}

// scanner is an interface matching the signature of (*sql.Rows).Scan.
type scanner interface {
	Scan(dest ...any) error
}

// queryList produces []T from a type which has a method that can scan rows into
// itself.
func queryList[T any](
	ctx context.Context,
	s *Storage,
	// A method expression which allows *T to scan data into itself.
	scan func(t *T, s scanner) error,
	// The query to perform and any associated prepared arguments.
	query string,
	args ...any,
) ([]T, error) {
	var ts []T
	err := s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		tmp, err := queryListTx(ctx, tx, scan, query, args...)
		if err != nil {
			return err
		}

		ts = tmp
		return nil
	})
	if err != nil {
		return nil, err
	}

	return ts, nil
}

// queryListTx produces []T from a type which has a method that can scan rows
// into itself, within the context of an existing transaction.
func queryListTx[T any](
	ctx context.Context,
	tx *sql.Tx,
	// A method expression which allows *T to scan data into itself.
	scan func(t *T, s scanner) error,
	// The query to perform and any associated prepared arguments.
	query string,
	args ...any,
) ([]T, error) {
	var ts []T
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query for %T: %v", ts, err)
	}
	defer rows.Close()

	for rows.Next() {
		var t T
		if err := scan(&t, rows); err != nil {
			return nil, fmt.Errorf("failed to scan for %T: %v", t, err)
		}
		ts = append(ts, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate rows for %T: %v", ts, err)
	}

	return ts, nil
}
