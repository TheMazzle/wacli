package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Polls staan in twee eigen tabellen naast messages: de poll zelf (vraag en
// opties) en per deelnemer de actuele stem. Het bericht van de poll staat
// gewoon in messages, met event_type 'poll' en de vraag als tekst, zodat
// zoeken, chatvoorbeeld en oudere Whatslack-versies blijven werken.
//
// Stemmen bewaren we als hex van de SHA-256-hash per optie, precies zoals
// WhatsApp ze stuurt. options_json bevat per optie naam én hash, dus een lezer
// koppelt stem en optie zonder zelf te hashen. Een stem die binnenkomt vóór
// de poll bekend is (de poll ging verloren of komt later via history) blijft
// zo ook bruikbaar.

// PollOption is één keuze in een poll.
type PollOption struct {
	Name string `json:"name"`
	Hash string `json:"hash"` // hex(SHA-256(name))
}

// Poll is een opgeslagen poll.
type Poll struct {
	ChatJID         string
	MsgID           string
	Question        string
	Options         []PollOption
	SelectableCount uint32
}

// PollVoteRow is de actuele stem van één deelnemer.
type PollVoteRow struct {
	ChatJID      string
	PollMsgID    string
	VoterJID     string
	OptionHashes []string // hex
	TS           int64
}

// PollOptionHash geeft de hash waarmee WhatsApp naar een optie verwijst.
func PollOptionHash(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func (d *DB) ensurePollTables() error {
	if _, err := d.sql.Exec(`
		CREATE TABLE IF NOT EXISTS polls (
			chat_jid TEXT NOT NULL,
			msg_id TEXT NOT NULL,
			question TEXT NOT NULL,
			options_json TEXT NOT NULL,
			selectable_count INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (chat_jid, msg_id)
		);
		CREATE TABLE IF NOT EXISTS poll_votes (
			chat_jid TEXT NOT NULL,
			poll_msg_id TEXT NOT NULL,
			voter_jid TEXT NOT NULL,
			option_hashes_json TEXT NOT NULL,
			ts INTEGER NOT NULL,
			PRIMARY KEY (chat_jid, poll_msg_id, voter_jid)
		);
	`); err != nil {
		return fmt.Errorf("create poll tables: %w", err)
	}
	return nil
}

// UpsertPoll legt een poll vast. De hashes worden hier berekend uit de namen.
func (d *DB) UpsertPoll(chatJID, msgID, question string, options []string, selectable uint32) error {
	if chatJID == "" || msgID == "" {
		return fmt.Errorf("chat JID and msg ID are required")
	}
	opts := make([]PollOption, 0, len(options))
	for _, name := range options {
		opts = append(opts, PollOption{Name: name, Hash: PollOptionHash(name)})
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`
		INSERT INTO polls(chat_jid, msg_id, question, options_json, selectable_count)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(chat_jid, msg_id) DO UPDATE SET
			question = excluded.question,
			options_json = excluded.options_json,
			selectable_count = excluded.selectable_count
	`, chatJID, msgID, question, string(raw), int64(selectable))
	return err
}

// GetPoll leest een poll. Onbekend: IsNotFound(err) is true.
func (d *DB) GetPoll(chatJID, msgID string) (Poll, error) {
	var p Poll
	var raw string
	var sel int64
	err := d.sql.QueryRow(`SELECT chat_jid, msg_id, question, options_json, selectable_count
		FROM polls WHERE chat_jid = ? AND msg_id = ?`, chatJID, msgID).
		Scan(&p.ChatJID, &p.MsgID, &p.Question, &raw, &sel)
	if err != nil {
		return Poll{}, err
	}
	p.SelectableCount = uint32(sel)
	if err := json.Unmarshal([]byte(raw), &p.Options); err != nil {
		return Poll{}, fmt.Errorf("decode poll options: %w", err)
	}
	return p, nil
}

// FindPoll zoekt een poll op bericht-ID, in welke chat ook. Handig omdat een
// eigen chat zowel onder het telefoonnummer als onder de LID kan staan.
func (d *DB) FindPoll(msgID string) (Poll, error) {
	var chat string
	err := d.sql.QueryRow(`SELECT chat_jid FROM polls WHERE msg_id = ? LIMIT 1`, msgID).Scan(&chat)
	if err != nil {
		return Poll{}, err
	}
	return d.GetPoll(chat, msgID)
}

// RecordPollVote legt de stem van voterJID vast. Een oudere stem dan de
// bewaarde wordt genegeerd: WhatsApp kan stemmen dubbel of in een andere
// volgorde afleveren (live én via history).
func (d *DB) RecordPollVote(chatJID, pollMsgID, voterJID string, hashes [][]byte, ts int64) error {
	if chatJID == "" || pollMsgID == "" || voterJID == "" {
		return fmt.Errorf("chat JID, poll msg ID and voter JID are required")
	}
	hx := make([]string, 0, len(hashes))
	for _, h := range hashes {
		hx = append(hx, hex.EncodeToString(h))
	}
	raw, err := json.Marshal(hx)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`
		INSERT INTO poll_votes(chat_jid, poll_msg_id, voter_jid, option_hashes_json, ts)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(chat_jid, poll_msg_id, voter_jid) DO UPDATE SET
			option_hashes_json = excluded.option_hashes_json,
			ts = excluded.ts
		WHERE excluded.ts >= poll_votes.ts
	`, chatJID, pollMsgID, voterJID, string(raw), ts)
	return err
}

// ListPollVotes geeft alle actuele stemmen op een poll.
func (d *DB) ListPollVotes(chatJID, pollMsgID string) ([]PollVoteRow, error) {
	rows, err := d.sql.Query(`SELECT chat_jid, poll_msg_id, voter_jid, option_hashes_json, ts
		FROM poll_votes WHERE chat_jid = ? AND poll_msg_id = ? ORDER BY ts`, chatJID, pollMsgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PollVoteRow
	for rows.Next() {
		var v PollVoteRow
		var raw string
		if err := rows.Scan(&v.ChatJID, &v.PollMsgID, &v.VoterJID, &raw, &v.TS); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &v.OptionHashes); err != nil {
			return nil, fmt.Errorf("decode vote: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
