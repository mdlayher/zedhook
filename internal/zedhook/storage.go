package zedhook

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

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
	createEventsSchemaQuery = `CREATE TABLE IF NOT EXISTS events (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id  INTEGER NOT NULL,
		timestamp INTEGER NOT NULL,
		class     TEXT NOT NULL,
		zpool     TEXT NOT NULL
	);`

	listEventsQuery = `SELECT * FROM events`

	saveEventQuery = `INSERT INTO events (
		event_id, timestamp, class, zpool
	) VALUES (?, ?, ?, ?);`
)

// ListEvents implements Storage.
func (s *sqlStorage) ListEvents(ctx context.Context) ([]Event, error) {
	var events []Event
	err := s.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, listEventsQuery)
		if err != nil {
			return fmt.Errorf("failed to query events: %v", err)
		}

		for rows.Next() {
			var (
				e    Event
				unix int64
			)

			if err := rows.Scan(&e.ID, &e.EventID, &unix, &e.Class, &e.Zpool); err != nil {
				return fmt.Errorf("failed to scan event: %v", err)
			}

			e.Timestamp = time.Unix(0, unix)
			events = append(events, e)
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("failed to iterate rows: %v", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return events, nil
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
