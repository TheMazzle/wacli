package store

import "fmt"

// ReceiptKind is wat een ontvanger over een bericht van ons meldt.
type ReceiptKind int

const (
	ReceiptDelivered ReceiptKind = iota
	ReceiptRead
	ReceiptPlayed
)

// MessageReceipt is de afleverstatus van één bericht bij één ontvanger.
// Een tijdstip van 0 betekent: nog niet gemeld.
type MessageReceipt struct {
	ChatJID     string
	MsgID       string
	UserJID     string
	DeliveredTS int64
	ReadTS      int64
	PlayedTS    int64
}

func (d *DB) ensureReceiptsTable() error {
	if _, err := d.sql.Exec(`
		CREATE TABLE IF NOT EXISTS message_receipts (
			chat_jid TEXT NOT NULL,
			msg_id TEXT NOT NULL,
			user_jid TEXT NOT NULL,
			delivered_ts INTEGER,
			read_ts INTEGER,
			played_ts INTEGER,
			PRIMARY KEY (msg_id, user_jid)
		);
	`); err != nil {
		return fmt.Errorf("create message_receipts: %w", err)
	}
	return nil
}

// RecordReceipt legt vast dat userJID bericht msgID heeft ontvangen, gelezen
// of afgespeeld. Gelezen houdt afgeleverd in en afgespeeld houdt beide in:
// WhatsApp stuurt de eerdere stappen niet altijd apart. Per stap blijft het
// vroegste tijdstip staan, want elk apparaat van de ontvanger meldt zich los.
func (d *DB) RecordReceipt(chatJID, msgID, userJID string, kind ReceiptKind, ts int64) error {
	if msgID == "" || userJID == "" {
		return fmt.Errorf("msg ID and user JID are required")
	}
	var delivered, read, played any
	delivered = ts
	if kind >= ReceiptRead {
		read = ts
	}
	if kind >= ReceiptPlayed {
		played = ts
	}
	_, err := d.sql.Exec(`
		INSERT INTO message_receipts(chat_jid, msg_id, user_jid, delivered_ts, read_ts, played_ts)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(msg_id, user_jid) DO UPDATE SET
			delivered_ts = MIN(COALESCE(delivered_ts, excluded.delivered_ts), COALESCE(excluded.delivered_ts, delivered_ts)),
			read_ts      = MIN(COALESCE(read_ts, excluded.read_ts), COALESCE(excluded.read_ts, read_ts)),
			played_ts    = MIN(COALESCE(played_ts, excluded.played_ts), COALESCE(excluded.played_ts, played_ts))
	`, chatJID, msgID, userJID, delivered, read, played)
	return err
}

// GetMessageReceipts geeft de afleverstatus van een bericht per ontvanger,
// in volgorde van eerste melding.
func (d *DB) GetMessageReceipts(msgID string) ([]MessageReceipt, error) {
	rows, err := d.sql.Query(`
		SELECT chat_jid, msg_id, user_jid, COALESCE(delivered_ts, 0), COALESCE(read_ts, 0), COALESCE(played_ts, 0)
		FROM message_receipts
		WHERE msg_id = ?
		ORDER BY COALESCE(delivered_ts, read_ts), user_jid
	`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageReceipt
	for rows.Next() {
		var r MessageReceipt
		if err := rows.Scan(&r.ChatJID, &r.MsgID, &r.UserJID, &r.DeliveredTS, &r.ReadTS, &r.PlayedTS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
