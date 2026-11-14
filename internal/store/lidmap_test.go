package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCopyLIDMap(t *testing.T) {
	db := openTestDB(t)
	sessPath := filepath.Join(t.TempDir(), "session.db")
	sess, err := sql.Open("sqlite3", sessPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Exec(`CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL);
		INSERT INTO whatsmeow_lid_map VALUES ('181870423715997','31637172862'), ('2','31600000002');`); err != nil {
		t.Fatal(err)
	}
	sess.Close()

	n, err := db.CopyLIDMap(context.Background(), sessPath)
	if err != nil || n != 2 {
		t.Fatalf("CopyLIDMap = %d, %v", n, err)
	}
	var pn string
	if err := db.sql.QueryRow(`SELECT pn FROM lid_map WHERE lid='181870423715997@lid'`).Scan(&pn); err != nil || pn != "31637172862@s.whatsapp.net" {
		t.Fatalf("pn = %q, %v", pn, err)
	}
	// Nogmaals kopiëren is veilig.
	if n, err := db.CopyLIDMap(context.Background(), sessPath); err != nil || n != 2 {
		t.Fatalf("second copy = %d, %v", n, err)
	}
}
