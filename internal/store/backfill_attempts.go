package store

import (
	"fmt"
	"time"
)

func (d *DB) ensureBackfillAttemptsTable() error {
	if _, err := d.sql.Exec(`
		CREATE TABLE IF NOT EXISTS backfill_attempts (
			chat_jid TEXT NOT NULL,
			after_ts INTEGER NOT NULL,
			requested_at INTEGER NOT NULL,
			PRIMARY KEY (chat_jid, after_ts)
		);
	`); err != nil {
		return fmt.Errorf("create backfill_attempts: %w", err)
	}
	return nil
}

// RecordBackfillAttempt onthoudt dat voor het gat dat eindigt op afterTS in
// dit gesprek geschiedenis bij de telefoon is opgevraagd.
func (d *DB) RecordBackfillAttempt(chatJID string, afterTS int64, at time.Time) error {
	_, err := d.sql.Exec(`
		INSERT INTO backfill_attempts(chat_jid, after_ts, requested_at) VALUES(?, ?, ?)
		ON CONFLICT(chat_jid, after_ts) DO UPDATE SET requested_at = excluded.requested_at
	`, chatJID, afterTS, at.UTC().Unix())
	return err
}

// BackfillAttemptedSince meldt of dit gat sinds `since` al is opgevraagd.
func (d *DB) BackfillAttemptedSince(chatJID string, afterTS int64, since time.Time) (bool, error) {
	var n int
	err := d.sql.QueryRow(`
		SELECT COUNT(1) FROM backfill_attempts
		WHERE chat_jid = ? AND after_ts = ? AND requested_at >= ?
	`, chatJID, afterTS, since.UTC().Unix()).Scan(&n)
	return n > 0, err
}

// LastBackfillAttempt geeft het tijdstip van het laatste verzoek, of de
// nultijd als er nog nooit een is gedaan.
func (d *DB) LastBackfillAttempt() (time.Time, error) {
	var ts int64
	if err := d.sql.QueryRow(`SELECT COALESCE(MAX(requested_at), 0) FROM backfill_attempts`).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	if ts == 0 {
		return time.Time{}, nil
	}
	return time.Unix(ts, 0).UTC(), nil
}
