package store

// Herstel van chatnamen die nooit zijn opgelost.
//
// ResolveChatName viel terug op de rauwe JID als er geen naam bekend was.
// Voor LID-adressen leverde dat onleesbare namen op ("152136902033557@lid"),
// terwijl de LID->telefoonnummer mapping lokaal wel beschikbaar was.

// ChatsWithUnresolvedNames geeft de chats waarvan de naam geen echte naam is:
// leeg, gelijk aan de volledige JID, of gelijk aan alleen het gebruikersdeel.
//
// Chats met een echte naam blijven buiten schot, zodat een herstelpas nooit een
// goede naam overschrijft.
func (d *DB) ChatsWithUnresolvedNames() ([]Chat, error) {
	const q = `
		SELECT jid, kind, COALESCE(name,''), COALESCE(last_message_ts,0)
		FROM chats
		WHERE name IS NULL
		   OR TRIM(name) = ''
		   OR name = jid
		   OR name = SUBSTR(jid, 1, INSTR(jid, '@') - 1)
		ORDER BY last_message_ts DESC`
	rows, err := d.sql.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Chat
	for rows.Next() {
		var c Chat
		var ts int64
		if err := rows.Scan(&c.JID, &c.Kind, &c.Name, &ts); err != nil {
			return nil, err
		}
		c.LastMessageTS = fromUnix(ts)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetChatName werkt alleen de naam bij en laat de rest van de rij ongemoeid.
func (d *DB) SetChatName(jid, name string) error {
	_, err := d.sql.Exec(`UPDATE chats SET name = ? WHERE jid = ?`, name, jid)
	return err
}

// LocalGroupName geeft de opgeslagen groepsnaam, of een lege string als die er
// niet is. Bewust lokaal: GetGroupInfo doet een netwerkronde per groep, wat bij
// honderden groepen op rate limits stukloopt terwijl de naam al in de
// groups-tabel staat.
func (d *DB) LocalGroupName(jid string) string {
	var name string
	row := d.sql.QueryRow(`SELECT COALESCE(name,'') FROM groups WHERE jid = ?`, jid)
	if err := row.Scan(&name); err != nil {
		return ""
	}
	return name
}
