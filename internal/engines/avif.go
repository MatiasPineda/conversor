package engines

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gen2brain/avif"

	"conversor/internal/codec"
)

// AVIF encoding is slow and memory-hungry in WebAssembly (a 24-megapixel
// photo takes tens of seconds and gigabytes). When FFmpeg with libaom is
// installed, opaque images go through it natively instead; images with
// transparency, or machines without it, use the embedded encoder, one at a
// time so parallel jobs can't exhaust memory.

var (
	wasmAVIF  sync.Mutex
	aomOnce   sync.Once
	aomFFmpeg string
)

// libaomFFmpeg is an FFmpeg that has the libaom AV1 encoder, or "".
func libaomFFmpeg() string {
	aomOnce.Do(func() {
		ff := FFmpegPath()
		if ff == "" {
			return
		}
		cmd := exec.Command(ff, "-hide_banner", "-encoders")
		prepareCommand(cmd)
		out, err := cmd.Output()
		if err == nil && strings.Contains(string(out), "libaom-av1") {
			aomFFmpeg = ff
		}
	})
	return aomFFmpeg
}

func encodeAVIF(w io.Writer, img image.Image, quality int) error {
	if ff := libaomFFmpeg(); ff != "" && codec.IsOpaque(img) {
		return encodeAVIFWithFFmpeg(ff, w, img, quality)
	}
	wasmAVIF.Lock()
	defer wasmAVIF.Unlock()
	return avif.Encode(w, codec.ToNRGBA(img), avif.Options{Quality: quality, QualityAlpha: quality, Speed: 10})
}

// encodeAVIFWithFFmpeg pipes raw RGB to FFmpeg. Quality maps onto libaom's
// CRF the way libavif maps its quality: 100 is lossless-ish, 0 the worst.
func encodeAVIFWithFFmpeg(ff string, w io.Writer, img image.Image, quality int) error {
	src := codec.ToNRGBA(img)
	b := src.Bounds()
	rgb := make([]byte, 0, b.Dx()*b.Dy()*3)
	for i := 0; i < len(src.Pix); i += 4 {
		rgb = append(rgb, src.Pix[i], src.Pix[i+1], src.Pix[i+2])
	}
	crf := int(math.Round(float64(100-quality) * 63 / 100))
	// The AVIF muxer needs a seekable file, not a pipe.
	tmp, err := os.CreateTemp("", "conversor-*.avif")
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := exec.Command(ff, "-hide_banner", "-nostdin", "-y", "-v", "error",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-s", fmt.Sprintf("%dx%d", b.Dx(), b.Dy()), "-i", "pipe:0",
		"-c:v", "libaom-av1", "-still-picture", "1", "-cpu-used", "6", "-row-mt", "1", "-crf", fmt.Sprint(crf),
		"-pix_fmt", "yuv420p", "-f", "avif", tmp.Name())
	prepareCommand(cmd)
	cmd.Stdin = bytes.NewReader(rgb)
	data, err := func() ([]byte, error) {
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		return os.ReadFile(tmp.Name())
	}()
	if err != nil || len(data) == 0 {
		// Fall back to the embedded encoder rather than failing.
		wasmAVIF.Lock()
		defer wasmAVIF.Unlock()
		return avif.Encode(w, src, avif.Options{Quality: quality, QualityAlpha: quality, Speed: 10})
	}
	_, err = w.Write(data)
	return err
}
