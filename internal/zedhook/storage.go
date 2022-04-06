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
	"fmt"
	"io"

	_ "modernc.org/sqlite"
)

// TODO(mdlayher): consider adding a transactional interface to Storage if we
// need to bulk-insert data.

// TODO(mdlayher): pagination support for ListEvents.

// Storage is the interface implemented by persistent storage types for
// zedhookd.
type Storage interface {
	io.Closer
	ListEvents(ctx context.Context) ([]Event, error)
	SaveEvent(ctx context.Context, event Event) error
}

// NewStorage creates a sqlite-backed Storage implementation using the input
// DSN.
func NewStorage(ctx context.Context, dsn string) (Storage, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	s := &sqlStorage{db: db}
	if err := s.setup(ctx); err != nil {
		return nil, fmt.Errorf("failed to create sqlite schema: %v", err)
	}

	return s, nil
}

var _ Storage = (*sqlStorage)(nil)

// A sqlStorage is a Storage implementation backed by sqlite3.
type sqlStorage struct{ db *sql.DB }

// Close implements Storage.
func (s *sqlStorage) Close() error { return s.db.Close() }

// Queries used by sqlStorage.
const (
	createEventsSchemaQuery = `--
	CREATE TABLE IF NOT EXISTS events (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id  INTEGER NOT NULL,
		timestamp INTEGER NOT NULL,
		class     TEXT NOT NULL,
		zpool     TEXT NOT NULL
	);`

	listEventsQuery = `--
	SELECT
		id, event_id, timestamp, class, zpool
	FROM events;
	`

	saveEventQuery = `--
	INSERT INTO events (
		event_id, timestamp, class, zpool
	) VALUES (?, ?, ?, ?);
	`
)

// ListEvents implements Storage.
func (s *sqlStorage) ListEvents(ctx context.Context) ([]Event, error) {
	return queryList(ctx, s, listEventsQuery, (*Event).scan)
}

// SaveEvent implements Storage.
func (s *sqlStorage) SaveEvent(ctx context.Context, e Event) error {
	return s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(
			ctx,
			saveEventQuery,
			e.ID,
			e.Timestamp.UnixNano(),
			e.Class,
			e.Zpool,
		)
		return err
	})
}

// setup creates the schema for sqlStorage.
func (s *sqlStorage) setup(ctx context.Context) error {
	return s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, createEventsSchemaQuery)
		return err
	})
}

// withTx begins a transaction and executes it within fn, either committing or
// rolling back the transaction depending on the return value of fn.
func (s *sqlStorage) withTx(
	ctx context.Context,
	fn func(ctx context.Context, tx *sql.Tx) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %v", err)
	}

	if err := fn(ctx, tx); err != nil {
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

// queryList produces []T from a type which has a method that can scan rows into
// itself.
func queryList[T any](
	ctx context.Context,
	s *sqlStorage,
	query string,
	// A method expression which allows *T to scan *sql.Rows data into itself.
	scan func(t *T, rows *sql.Rows) error,
) ([]T, error) {
	var ts []T
	err := s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to query for %T: %v", ts, err)
		}

		for rows.Next() {
			var t T
			if err := scan(&t, rows); err != nil {
				return fmt.Errorf("failed to scan for %T: %v", t, err)
			}
			ts = append(ts, t)
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("failed to iterate rows for %T: %v", ts, err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return ts, nil
}
