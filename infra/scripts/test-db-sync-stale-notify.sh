#!/bin/bash
# Bewijst dat de stale-run guard in wacli-db-sync.sh een notificatie stuurt
# wanneer hij een vastgelopen run opruimt — niet alleen een logregel schrijft
# (die logregel bestond al vóór deze taak en zegt niets over de notify-call).
#
# Deze test raakte eerder per ongeluk de ECHTE database aan: na de guard
# loopt het script door naar de bereikbaarheidscheck met het hardcoded
# REMOTE="macmini", en als dat toevallig oploste (self-loop, mDNS, een
# toekomstige /etc/hosts-wijziging) volgde een echte `sqlite3 .backup` en
# `rsync` over ~/.wacli/wacli.db — terwijl de daemon die open houdt in WAL-
# modus. Dat is nooit de bedoeling van deze test: het onderwerp is de guard
# en zijn notificatie, niet de sync zelf. Daarom:
#   1. REMOTE wordt overschreven naar een gegarandeerd onbereikbare host
#      (een `.invalid`-domein, RFC 2606 — resolvt nergens, nooit, ongeacht
#      lokale DNS/mDNS-eigenaardigheden). Het script stopt dan zelf bij zijn
#      eigen bereikbaarheidscheck, vóór de backup/rsync.
#   2. LOG_FILE wordt overschreven naar een temp-pad. De logrotatie-check in
#      wacli-db-sync.sh loopt VÓÓR de guard en de bereikbaarheidscheck — dus
#      buiten de "het script stopt vroeg"-bescherming van punt 1 — en stat't
#      (en bij >10 MB: roteert) onvoorwaardelijk elke run. Zonder deze
#      override zou elke testrun de ECHTE ~/Library/Logs/Whatslack/
#      wacli-sync.log lezen, en bij toeval roteren.
#   3. Een safety-net-assertie ná de run op de ECHTE bestanden — niet als
#      aanname, maar als test op zichzelf: als een toekomstige wijziging dit
#      ooit weer doorbreekt, faalt de test daarop, niet pas bij een
#      handmatige controle. Voor de sync-log (nu volledig buiten bereik door
#      punt 2) is exacte gelijkheid van mtime/grootte/regelaantal een
#      correcte eis. Voor de database ligt dat anders: de wacli-daemon draait
#      live door en voegt tijdens de testrun legitiem nieuwe berichten toe
#      (WAL-modus: commits zijn direct zichtbaar voor een read-only query),
#      dus exacte gelijkheid zou vals falen op puur toeval — precies het
#      soort waarschuwingssysteem dat mensen leren negeren. In plaats daarvan
#      mag de database GROEIEN maar nooit KRIMPEN (bytegrootte én
#      berichtenaantal); alleen krimp wijst op iets dat de database heeft
#      overschreven. mtime wordt niet gecontroleerd: WAL-checkpoints van de
#      live daemon verplaatsen die sowieso, los van of er schade is.
#
# Gebruikt verder een tijdelijk LOCK-bestand (niet het echte
# ~/.wacli/.db-sync.lock) en een stub-NOTIFY die alleen zijn argumenten
# wegschrijft in plaats van een echte macOS-notificatie te versturen.
set -uo pipefail

DBSYNC="$HOME/Projects/wacli/infra/scripts/wacli-db-sync.sh"
REAL_DB="$HOME/.wacli/wacli.db"
REAL_SYNC_LOG="$HOME/Library/Logs/Whatslack/wacli-sync.log"
WORK=$(mktemp -d /tmp/dbsync-stale-test.XXXXXX)
DUMMY_PID=""
cleanup() {
    rm -rf "$WORK"
    [[ -n "$DUMMY_PID" ]] && kill -9 "$DUMMY_PID" 2>/dev/null
    [[ -n "$DUMMY_PID" ]] && wait "$DUMMY_PID" 2>/dev/null
    return 0
}
trap cleanup EXIT

fail() { echo "!! $1" >&2; exit 1; }

# --- Helpers voor de database-safety-net. Nemen een pad als argument zodat
# ze ook tegen een synthetische testdatabase gedraaid kunnen worden (zie
# zelftest hieronder) — nooit hardcoded tegen $REAL_DB, om aantoonbaar te
# maken dat de logica zelf klopt zonder de echte database te hoeven
# beschadigen. ---
db_snapshot() {
    local db_path="$1"
    [[ -f "$db_path" ]] || { echo "0 0"; return; }
    local size count
    size=$(stat -f%z "$db_path" 2>/dev/null || echo 0)
    count=$(sqlite3 -cmd ".timeout 5000" "file:$db_path?mode=ro" \
        "SELECT COUNT(*) FROM messages;" 2>/dev/null || echo 0)
    echo "$size $count"
}
# Waar (exit 0) als "after" niet gekrompen is t.o.v. "before" — noch in
# bytegrootte, noch in berichtenaantal. Groei is toegestaan (de daemon voegt
# legitiem berichten toe); alleen krimp is een signaal van schade.
db_not_shrunk() {
    local before="$1" after="$2"
    local size_before count_before size_after count_after
    read -r size_before count_before <<< "$before"
    read -r size_after count_after <<< "$after"
    (( size_after >= size_before )) && (( count_after >= count_before ))
}
log_snapshot() {
    local f="$1"
    [[ -f "$f" ]] || { echo "0 0 0"; return; }
    echo "$(stat -f%m "$f" 2>/dev/null || echo 0) $(stat -f%z "$f" 2>/dev/null || echo 0) $(grep -c "" "$f" 2>/dev/null || echo 0)"
}

# --- Zelftest van db_not_shrunk(), met een synthetische database (nooit de
# echte wacli.db) — bewijst dat de shrink-detectie een genuine beschadiging
# wél opmerkt, en normale groei niet ten onrechte afkeurt. ---
SELFTEST_DB="$WORK/selftest.db"
sqlite3 "$SELFTEST_DB" "CREATE TABLE messages (ts INTEGER); INSERT INTO messages VALUES (1),(2),(3);" \
    || fail "zelftest: kon synthetische database niet aanmaken"
SNAP_BASELINE=$(db_snapshot "$SELFTEST_DB")

sqlite3 "$SELFTEST_DB" "INSERT INTO messages VALUES (4);"
SNAP_GROWN=$(db_snapshot "$SELFTEST_DB")
db_not_shrunk "$SNAP_BASELINE" "$SNAP_GROWN" \
    || fail "zelftest: groei van de database werd ten onrechte afgekeurd — de shrink-detectie is te streng"
echo "==> Zelftest OK: groei van de database ($SNAP_BASELINE -> $SNAP_GROWN) wordt niet afgekeurd"

# Krimp simuleren op de synthetische kopie (rijen verwijderen) — NOOIT op de
# echte database.
sqlite3 "$SELFTEST_DB" "DELETE FROM messages;"
SNAP_SHRUNK=$(db_snapshot "$SELFTEST_DB")
if db_not_shrunk "$SNAP_GROWN" "$SNAP_SHRUNK"; then
    fail "zelftest: een gekrompen database (rijen verwijderd) werd NIET afgekeurd — de shrink-detectie werkt niet"
fi
echo "==> Zelftest OK: krimp van de database ($SNAP_GROWN -> $SNAP_SHRUNK) wordt wél afgekeurd"

# --- Nu de echte test: snapshot van de ECHTE bestanden vóór de run. ---
DB_BEFORE=$(db_snapshot "$REAL_DB")
SYNCLOG_BEFORE=$(log_snapshot "$REAL_SYNC_LOG")

# Directe controle dat de LOG_FILE-override ook echt wordt gehonoreerd (niet
# alleen dat het ECHTE bestand met rust gelaten wordt — dat zou ook waar zijn
# als de override genegeerd werd en de ECHTE log toevallig klein genoeg is
# om nooit te roteren). Zet een oversized bestand (>10 MB) neer op het
# TEMP-pad en verwacht dat precies dát bestand roteert.
FAKE_LOG_MARKER="OUDE-SYNC-LOG-REGEL-$(date +%s)"
python3 -c "
with open('$WORK/wacli-sync.log', 'wb') as f:
    f.write(('$FAKE_LOG_MARKER\n').encode())
    f.write(b'x' * (10 * 1024 * 1024 + 100))
"
FAKE_LOG_ORIG_SIZE=$(stat -f%z "$WORK/wacli-sync.log")
(( FAKE_LOG_ORIG_SIZE > 10 * 1024 * 1024 )) \
    || fail "test-fixture voor de LOG_FILE-override is niet groot genoeg (${FAKE_LOG_ORIG_SIZE} bytes)"

# Stub-notifier: schrijft alleen weg dat en waarmee hij aangeroepen werd.
NOTIFY_STUB="$WORK/fake-notify.sh"
cat > "$NOTIFY_STUB" <<'STUB'
#!/bin/bash
echo "CALLED:$*" >> "$NOTIFY_LOG"
exit 0
STUB
chmod +x "$NOTIFY_STUB"
NOTIFY_LOG="$WORK/notify.log"
export NOTIFY_LOG

# Fake vastgelopen run: een echt (kortlevend, onschadelijk) sleep-proces
# waarvan de PID en een starttijd ver in het verleden in het LOCK-bestand
# staan, zodat AGE > MAX_RUN_SECONDS.
sleep 60 &
DUMMY_PID=$!
disown "$DUMMY_PID" 2>/dev/null || true
LOCK_FILE="$WORK/db-sync.lock"
printf '%s\n%s\n' "$DUMMY_PID" "$(( $(date +%s) - 400 ))" > "$LOCK_FILE"

MAX_RUN_SECONDS=300 LOCK="$LOCK_FILE" NOTIFY="$NOTIFY_STUB" \
    NOTIFY_LOG="$NOTIFY_LOG" REMOTE="dbsync-test-unreachable.invalid" \
    LOG_FILE="$WORK/wacli-sync.log" \
    bash "$DBSYNC" >"$WORK/stdout.log" 2>&1

grep -q "vorige run (pid $DUMMY_PID) hangt al" "$WORK/stdout.log" \
    || fail "de guard heeft de stale run niet als zodanig herkend (log ontbreekt)"

kill -0 "$DUMMY_PID" 2>/dev/null \
    && fail "de stale run is niet opgeruimd; het dummy-proces leeft nog"

[[ -f "$NOTIFY_LOG" ]] \
    || fail "de stub-notifier is niet aangeroepen; de stale-run guard ruimt weer stilzwijgend op"

grep -q "opgeruimd" "$NOTIFY_LOG" \
    || fail "de notificatie kwam wel binnen, maar de inhoud klopt niet: $(cat "$NOTIFY_LOG" 2>/dev/null)"

# Bewijs dat het script echt bij de bereikbaarheidscheck stopte en nooit een
# echte sync probeerde.
grep -q "unreachable" "$WORK/stdout.log" \
    || fail "geen 'unreachable'-melding gezien; onduidelijk of het script bij de bereikbaarheidscheck stopte"
grep -qE "db synced|media synced" "$WORK/stdout.log" \
    && fail "het script meldt een geslaagde db/media-sync — dat had nooit mogen gebeuren in deze test"

echo "==> Stale-run notificatie OK: $(cat "$NOTIFY_LOG")"

# Bewijs dat LOG_FILE echt gehonoreerd werd: het oversized bestand op het
# TEMP-pad moet geroteerd zijn naar .1, mét de oude marker erin.
[[ -f "$WORK/wacli-sync.log.1" ]] \
    || fail "geen .1-bestand op het TEMP-pad na een oversized log; LOG_FILE-override werkt niet (het script roteerde mogelijk de ECHTE log, of niets)"
grep -q "$FAKE_LOG_MARKER" "$WORK/wacli-sync.log.1" \
    || fail "de oude inhoud staat niet in het geroteerde TEMP-bestand"
echo "==> LOG_FILE-override OK: het oversized TEMP-bestand (${FAKE_LOG_ORIG_SIZE} bytes) roteerde naar .1"

# --- Safety-net: de ECHTE bestanden mogen niet zijn aangeraakt/beschadigd. ---
DB_AFTER=$(db_snapshot "$REAL_DB")
SYNCLOG_AFTER=$(log_snapshot "$REAL_SYNC_LOG")

db_not_shrunk "$DB_BEFORE" "$DB_AFTER" \
    || fail "de ECHTE ~/.wacli/wacli.db lijkt beschadigd: bytegrootte/berichtenaantal is GEKROMPEN tijdens de test ([$DB_BEFORE] -> [$DB_AFTER]). Groei door de live daemon is normaal, krimp niet."

[[ "$SYNCLOG_BEFORE" == "$SYNCLOG_AFTER" ]] \
    || fail "de ECHTE wacli-sync.log (mtime size regels) is veranderd tijdens de test! vóór: [$SYNCLOG_BEFORE] na: [$SYNCLOG_AFTER] — LOG_FILE-override werkt niet"

echo "==> Safety-net OK: echte db niet gekrompen ($DB_BEFORE -> $DB_AFTER), echte sync-log volledig ongewijzigd ($SYNCLOG_AFTER)"
