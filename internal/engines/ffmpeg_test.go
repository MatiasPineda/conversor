package engines

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"conversor/internal/core"
)

// clip makes a 1-second 128×96 test video with a tone, or skips without
// FFmpeg.
func clip(t *testing.T) (*FFmpegEngine, string) {
	t.Helper()
	e := NewFFmpegEngine()
	if e.Unavailable() != "" || e.ffprobe == "" {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	input := filepath.Join(t.TempDir(), "clip.mp4")
	out, err := exec.Command(e.ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=128x96:rate=10:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", input).CombinedOutput()
	if err != nil {
		t.Fatalf("making a clip: %v %s", err, out)
	}
	return e, input
}

func probe(t *testing.T, e *FFmpegEngine, path, entries string) string {
	t.Helper()
	out, err := exec.Command(e.ffprobe, "-v", "error", "-show_entries", entries, "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestFFmpegHeightCapKeepsEvenDimensions(t *testing.T) {
	e, input := clip(t)
	for _, to := range []string{"mov", "gif"} {
		for height, want := range map[int]string{480: "128,96", 48: "64,48"} {
			out := run(t, e, input, to, core.Options{VideoHeight: core.Ptr(height)})
			if got := probe(t, e, out[0], "stream=width,height"); got != want {
				t.Fatalf("%s capped at %d: %s, want %s", to, height, got, want)
			}
		}
	}
}

func TestFFmpegCodecAndStripAudio(t *testing.T) {
	e, input := clip(t)
	hevc := core.HEVC
	for _, c := range []struct {
		o     core.Options
		codec string
		audio bool
	}{
		{core.Options{}, "h264", true},
		{core.Options{VideoCodec: &hevc}, "hevc", true},
		{core.Options{StripAudio: true}, "h264", false},
	} {
		out := run(t, e, input, "mov", c.o)
		streams := strings.Split(probe(t, e, out[0], "stream=codec_name,codec_type"), "\n")
		if !slices.Contains(streams, c.codec+",video") {
			t.Fatalf("%v: %v", c.o, streams)
		}
		hasAudio := slices.ContainsFunc(streams, func(s string) bool { return strings.HasSuffix(s, ",audio") })
		if hasAudio != c.audio {
			t.Fatalf("%v: audio %v, want %v", c.o, hasAudio, c.audio)
		}
	}
}

func TestFFmpegRefusesPlaylistsThatReachOtherFiles(t *testing.T) {
	e, _ := clip(t)
	dir := t.TempDir()
	secret := filepath.Join(dir, "private.mp4")
	os.WriteFile(secret, []byte("private local file"), 0o644)
	for name, body := range map[string]string{
		"list.m3u8": "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\n" + secret + "\n#EXT-X-ENDLIST\n",
		"list.mp4":  "ffconcat version 1.0\nfile '" + secret + "'\n",
		"dash.mp4":  `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static"><Period><AdaptationSet><Representation id="1" bandwidth="1"><BaseURL>` + secret + `</BaseURL></Representation></AdaptationSet></Period></MPD>`,
	} {
		input := filepath.Join(dir, name)
		os.WriteFile(input, []byte(body), 0o644)
		if _, err := e.Convert(task("mp4", "png", core.Options{}), input, t.TempDir()); err == nil {
			t.Fatalf("%s converted, reaching another file", name)
		}
	}
}

func TestVideoFrameToJPEG(t *testing.T) {
	e, input := clip(t)
	out := run(t, e, input, "jpeg", core.Options{})
	if b := decode(t, out[0]).Bounds(); b.Dx() != 128 || b.Dy() != 96 {
		t.Fatalf("frame %v", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(out[0])); len(entries) != 1 {
		t.Fatal("the intermediate frame was left behind")
	}
}
