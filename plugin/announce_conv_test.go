package plugin

import (
	"os"
	"testing"
)

// TestMP3ConversionAgainstReference converts CONVTEST_MP3 and writes the PCM
// next to it as <file>.got.pcm for comparison against an ffmpeg render.
func TestMP3ConversionAgainstReference(t *testing.T) {
	src := os.Getenv("CONVTEST_MP3")
	if src == "" {
		t.Skip("set CONVTEST_MP3 to an mp3 file")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := mp3ToSpeakerPCM(b, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm)%4 != 0 || len(pcm) == 0 {
		t.Fatalf("bad frame alignment: %d bytes", len(pcm))
	}
	if err := os.WriteFile(src+".got.pcm", pcm, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes (%.2fs at 48kHz stereo)", len(pcm), float64(len(pcm))/192000)
}
