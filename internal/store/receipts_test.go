package store

import "testing"

// Afleverstatus per bericht en per ontvanger: de basis voor de vinkjes in
// Whatslack (✓ verstuurd, ✓✓ afgeleverd, blauw gelezen) en het overzicht per
// groepslid.
func TestRecordReceiptStoresDeliveredThenRead(t *testing.T) {
	db := openTestDB(t)

	if err := db.RecordReceipt("g@g.us", "M1", "1@lid", ReceiptDelivered, 100); err != nil {
		t.Fatalf("RecordReceipt delivered: %v", err)
	}
	if err := db.RecordReceipt("g@g.us", "M1", "1@lid", ReceiptRead, 150); err != nil {
		t.Fatalf("RecordReceipt read: %v", err)
	}

	got, err := db.GetMessageReceipts("M1")
	if err != nil {
		t.Fatalf("GetMessageReceipts: %v", err)
	}
	if len(got) != 1 || got[0].UserJID != "1@lid" || got[0].DeliveredTS != 100 || got[0].ReadTS != 150 {
		t.Fatalf("receipts = %+v", got)
	}
}

// WhatsApp stuurt niet altijd eerst "afgeleverd": wie leest, heeft het ook ontvangen.
func TestRecordReceiptReadImpliesDelivered(t *testing.T) {
	db := openTestDB(t)

	if err := db.RecordReceipt("g@g.us", "M1", "1@lid", ReceiptRead, 150); err != nil {
		t.Fatalf("RecordReceipt: %v", err)
	}

	got, _ := db.GetMessageReceipts("M1")
	if len(got) != 1 || got[0].DeliveredTS != 150 || got[0].ReadTS != 150 {
		t.Fatalf("receipts = %+v", got)
	}
}

// Elk apparaat van een ontvanger meldt zich apart; het eerste tijdstip telt.
func TestRecordReceiptKeepsEarliestTimestamp(t *testing.T) {
	db := openTestDB(t)

	for _, ts := range []int64{200, 100, 300} {
		if err := db.RecordReceipt("g@g.us", "M1", "1@lid", ReceiptDelivered, ts); err != nil {
			t.Fatalf("RecordReceipt: %v", err)
		}
	}
	if err := db.RecordReceipt("g@g.us", "M1", "1@lid", ReceiptRead, 400); err != nil {
		t.Fatalf("RecordReceipt: %v", err)
	}

	got, _ := db.GetMessageReceipts("M1")
	if len(got) != 1 || got[0].DeliveredTS != 100 || got[0].ReadTS != 400 {
		t.Fatalf("receipts = %+v", got)
	}
}

func TestRecordReceiptPerRecipient(t *testing.T) {
	db := openTestDB(t)

	_ = db.RecordReceipt("g@g.us", "M1", "1@lid", ReceiptRead, 100)
	_ = db.RecordReceipt("g@g.us", "M1", "2@lid", ReceiptDelivered, 110)
	_ = db.RecordReceipt("g@g.us", "M2", "1@lid", ReceiptDelivered, 120)

	got, _ := db.GetMessageReceipts("M1")
	if len(got) != 2 {
		t.Fatalf("verwachtte 2 ontvangers voor M1, kreeg %+v", got)
	}
	if got[1].UserJID != "2@lid" || got[1].ReadTS != 0 {
		t.Fatalf("tweede ontvanger = %+v", got[1])
	}
}

func TestRecordReceiptPlayedImpliesReadAndDelivered(t *testing.T) {
	db := openTestDB(t)

	_ = db.RecordReceipt("u@s.whatsapp.net", "M1", "u@s.whatsapp.net", ReceiptPlayed, 500)

	got, _ := db.GetMessageReceipts("M1")
	if len(got) != 1 || got[0].DeliveredTS != 500 || got[0].ReadTS != 500 || got[0].PlayedTS != 500 {
		t.Fatalf("receipts = %+v", got)
	}
}
