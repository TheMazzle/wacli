package wa

import (
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestMediaTypeFromString(t *testing.T) {
	for _, tc := range []string{"image", "video", "audio", "document"} {
		if _, err := MediaTypeFromString(tc); err != nil {
			t.Fatalf("expected %s to be supported: %v", tc, err)
		}
	}
	if _, err := MediaTypeFromString("nope"); err == nil {
		t.Fatalf("expected error for unsupported type")
	}
}

// WhatsApp verstuurt een GIF als VideoMessage met gifPlayback; wacli slaat die
// op als media_type "gif". Zonder mapping faalde elke download met
// "unsupported media type: gif" en bleef de GIF een bijlage-kaartje.
func TestMediaTypeFromStringGifIsVideo(t *testing.T) {
	mt, err := MediaTypeFromString("gif")
	if err != nil {
		t.Fatalf("expected gif to be supported: %v", err)
	}
	if mt != whatsmeow.MediaVideo {
		t.Fatalf("expected gif to map to MediaVideo, got %q", mt)
	}
}
