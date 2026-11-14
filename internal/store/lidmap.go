package store

import (
	"context"
	"fmt"
	"os"
)

// De koppeling LID ↔ telefoonnummer staat in whatsmeow's session.db. Die gaat
// niet mee naar de MacBook (alleen wacli.db), terwijl Whatslack daar afzenders
// (vaak een LID) aan stemmen en contacten (vaak een nummer) moet koppelen.
// Daarom een kopie in wacli.db.

func (d *DB) ensureLIDMapTable() error {
	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS lid_map (
		lid TEXT PRIMARY KEY, -- "123@lid"
		pn  TEXT NOT NULL     -- "316…@s.whatsapp.net"
	)`); err != nil {
		return fmt.Errorf("create lid_map: %w", err)
	}
	return nil
}

// CopyLIDMap neemt de LID-koppelingen over uit session.db. Geeft het aantal
// rijen in lid_map terug.
func (d *DB) CopyLIDMap(ctx context.Context, sessionPath string) (int, error) {
	if _, err := os.Stat(sessionPath); err != nil {
		return 0, err
	}
	// ATTACH geldt per verbinding: één vaste verbinding uit de pool.
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS sess`, "file:"+sessionPath+"?mode=ro"); err != nil {
		return 0, fmt.Errorf("attach session.db: %w", err)
	}
	defer conn.ExecContext(context.Background(), `DETACH DATABASE sess`) //nolint:errcheck
	if _, err := conn.ExecContext(ctx, `
		INSERT OR REPLACE INTO lid_map(lid, pn)
		SELECT lid || '@lid', pn || '@s.whatsapp.net' FROM sess.whatsmeow_lid_map
		WHERE lid <> '' AND pn <> ''`); err != nil {
		return 0, fmt.Errorf("copy lid map: %w", err)
	}
	var n int
	err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM lid_map`).Scan(&n)
	return n, err
}
