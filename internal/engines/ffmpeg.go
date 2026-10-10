package engines

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

var (
	videoIn  = []string{"mp4", "mov", "webm", "mkv", "avi", "gif"}
	videoOut = []string{"mp4", "mov", "webm", "mkv", "avi", "gif"}
	// Video to image grabs the first frame. Other image formats go through
	// PNG and the image engine.
	frameOut = []string{"png", "jpeg"}
	audio    = []string{"mp3", "wav", "flac", "aac", "m4a", "ogg", "opus"}
)

// localInputArgs accept only self-contained media containers. Playlist
// demuxers (HLS, DASH, concat) can open other local files or URLs even when
// network protocols are disabled.
var localInputArgs = []string{
	"-protocol_whitelist", "file,pipe",
	"-format_whitelist", "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,avi,gif,mp3,wav,flac,aac,ogg",
}

func localPath(path string) string { return "file:" + path }

// FFmpegPath is the FFmpeg binary the engine would run: $CONVERSOR_FFMPEG,
// next to the executable, then PATH. The desktop app uses it for video
// thumbnails.
func FFmpegPath() string { return findTool([]string{"ffmpeg"}, "CONVERSOR_FFMPEG") }

// ThumbnailCommand grabs one frame at offset at, width pixels wide, as PNG
// on stdout.
func ThumbnailCommand(ffmpeg, path, at string, width int) *exec.Cmd {
	args := append(slices.Clone(localInputArgs), "-nostdin", "-v", "error", "-ss", at, "-i", localPath(path),
		"-frames:v", "1", "-an", "-sn", "-vf", fmt.Sprintf("scale=%d:-2", width), "-f", "image2pipe", "-c:v", "png", "-")
	cmd := exec.Command(ffmpeg, args...)
	prepareCommand(cmd)
	return cmd
}

// FFmpegEngine runs the FFmpeg binary as a child process, so a crash in a
// codec can't take the app down with it.
type FFmpegEngine struct {
	ffmpeg, ffprobe string
}

func NewFFmpegEngine() *FFmpegEngine {
	return &FFmpegEngine{
		ffmpeg:  FFmpegPath(),
		ffprobe: findTool([]string{"ffprobe"}, "CONVERSOR_FFPROBE"),
	}
}

func (*FFmpegEngine) ID() string    { return "ffmpeg" }
func (*FFmpegEngine) Priority() int { return 0 }

func (e *FFmpegEngine) Unavailable() string {
	if e.ffmpeg == "" {
		return i18n.T("ffmpeg not found; run `conversor pack install media` (or set CONVERSOR_FFMPEG)")
	}
	return ""
}

func (*FFmpegEngine) Steps() []core.Step {
	targets := slices.Concat(videoOut, audio, frameOut)
	var steps []core.Step
	for _, s := range core.Steps(videoIn, targets) {
		// GIF has no audio stream, so extraction is never meaningful.
		if s.From.ID == "gif" && slices.Contains(audio, s.To.ID) {
			continue
		}
		steps = append(steps, s)
	}
	return append(steps, core.Steps(audio, audio)...)
}

// durationMicros is the input's duration for progress, or 0 when ffprobe is
// missing or can't tell. Only cancellation is an error.
func (e *FFmpegEngine) durationMicros(t *core.Task, input string) (float64, error) {
	if e.ffprobe == "" {
		return 0, nil
	}
	args := append(slices.Clone(localInputArgs), "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", localPath(input))
	var secs float64
	err := runTool(t, "ffprobe", exec.Command(e.ffprobe, args...), func(line string) {
		if v, err := strconv.ParseFloat(strings.TrimSpace(line), 64); err == nil && secs == 0 {
			secs = v
		}
	})
	if core.IsCancelled(err) {
		return 0, err
	}
	if err != nil {
		return 0, nil
	}
	return secs * 1e6, nil
}

// crf maps 1-100 quality onto a CRF scale where quality 100 is best.
func crf(quality int, worst float64) string {
	return fmt.Sprintf("%.0f", worst-float64(quality)*0.25)
}

// videoScale caps height while keeping both dimensions even. Zero is a
// special FFmpeg size, so narrow frames are clamped to two; a one-pixel
// source or cap can't be made even without enlarging.
func videoScale(height int) string {
	return fmt.Sprintf("scale='if(lt(iw,2),nan,max(2,trunc(iw*min(1,%d/ih)/2)*2))':'if(lt(min(ih,%d),2),nan,max(2,trunc(min(ih,%d)/2)*2))'", height, height, height)
}

// outputArgs are the encoder arguments for one output format and options.
func outputArgs(to string, o *core.Options) []string {
	q := o.Quality
	x264CRF := "20"
	vp9CRF := "32"
	if q != nil {
		x264CRF = crf(*q, 40)
		vp9CRF = crf(*q, 52)
	}
	aac := fmt.Sprintf("%dk", core.Get(o.AudioBitrate, 192))
	bitrate := ""
	if o.AudioBitrate != nil {
		bitrate = fmt.Sprintf("%dk", *o.AudioBitrate)
	}
	var args []string
	push := func(a ...string) { args = append(args, a...) }
	switch to {
	case "mp4", "mov", "mkv":
		if core.Get(o.VideoCodec, core.H264) == core.HEVC {
			x265CRF := "24"
			if q != nil {
				x265CRF = crf(*q, 44)
			}
			push("-c:v", "libx265", "-preset", "medium", "-crf", x265CRF)
			// QuickTime and Apple devices only play HEVC tagged hvc1.
			if to != "mkv" {
				push("-tag:v", "hvc1")
			}
		} else {
			push("-c:v", "libx264", "-preset", "medium", "-crf", x264CRF)
		}
		push("-pix_fmt", "yuv420p")
		if !o.StripAudio {
			push("-c:a", "aac", "-b:a", aac)
		}
		if to != "mkv" {
			push("-movflags", "+faststart")
		}
	case "webm":
		push("-c:v", "libvpx-vp9", "-crf", vp9CRF, "-b:v", "0", "-row-mt", "1")
		// VP9 in RGB (gbrap, from a transparent GIF) is refused by newer
		// FFmpeg, and few players handle it anyway.
		push("-deadline", "good", "-cpu-used", "4", "-pix_fmt", "yuv420p")
		if !o.StripAudio {
			push("-c:a", "libopus")
			if bitrate != "" {
				push("-b:a", bitrate)
			}
		}
	case "avi":
		qv := 3
		if q != nil {
			qv = 2 + (100-*q)*29/99
		}
		push("-c:v", "mpeg4", "-q:v", strconv.Itoa(qv))
		if !o.StripAudio {
			push("-c:a", "libmp3lame")
			if bitrate != "" {
				push("-b:a", bitrate)
			}
		}
	case "gif":
		size := "scale='min(720,iw)':-2"
		if o.VideoHeight != nil {
			size = videoScale(*o.VideoHeight)
		}
		push("-vf", "fps=12,"+size+":flags=lanczos,split[a][b];[a]palettegen[p];[b][p]paletteuse", "-an")
		return args
	case "png":
		// Still frames are always written as PNG, keeping any alpha;
		// Convert then encodes other targets through the image engine.
		push("-frames:v", "1", "-update", "1")
		if o.MaxSize != nil {
			m := *o.MaxSize
			push("-vf", fmt.Sprintf("scale='min(iw,%d)':'min(ih,%d)':force_original_aspect_ratio=decrease", m, m))
		}
		return args
	case "mp3":
		if bitrate != "" {
			push("-vn", "-c:a", "libmp3lame", "-b:a", bitrate)
		} else {
			push("-vn", "-c:a", "libmp3lame", "-q:a", "2")
		}
	case "wav":
		push("-vn", "-c:a", "pcm_s16le")
	case "flac":
		// Otherwise FFmpeg may keep the first decoded frame's length as the
		// block size (47 samples from a gapless MP3), which Apple's decoder
		// refuses to open.
		push("-vn", "-c:a", "flac", "-frame_size", "4096")
	case "aac":
		push("-vn", "-c:a", "aac", "-b:a", aac, "-f", "adts")
	case "m4a":
		push("-vn", "-c:a", "aac", "-b:a", aac)
	case "ogg":
		if bitrate != "" {
			push("-vn", "-c:a", "libvorbis", "-b:a", bitrate)
		} else {
			push("-vn", "-c:a", "libvorbis", "-q:a", "5")
		}
	case "opus":
		b := bitrate
		if b == "" {
			b = "128k"
		}
		push("-vn", "-c:a", "libopus", "-b:a", b)
	}
	if o.StripAudio && slices.Contains(videoOut, to) {
		push("-an")
	}
	if o.VideoHeight != nil && slices.Contains(videoOut, to) {
		push("-vf", videoScale(*o.VideoHeight))
	}
	return args
}

// noAudioError names the cause when a silent video was sent to an audio
// format, instead of passing on FFmpeg's empty-output message. Targets are
// offered per format, so a silent video still lists audio ones.
func noAudioError(err error, to string) error {
	if ce, ok := err.(*core.Error); ok && ce.Kind == core.KindEngineFailed &&
		slices.Contains(audio, to) && strings.Contains(ce.Message, "does not contain any stream") {
		return core.EngineFailed("ffmpeg", "%s", i18n.T("this file has no audio track to convert"))
	}
	return err
}

func (e *FFmpegEngine) Convert(t *core.Task, input, outDir string) ([]string, error) {
	if e.ffmpeg == "" {
		return nil, core.EngineMissing("ffmpeg", i18n.T("ffmpeg not found"))
	}
	to := t.Step.To.ID
	if to == "gif" && t.Options.Background != nil && !t.Options.Background.Transparent {
		return nil, core.InvalidOption("%s", i18n.T("a background color isn't supported for video to GIF yet"))
	}
	total, err := e.durationMicros(t, input)
	if err != nil {
		return nil, err
	}
	if total <= 0 {
		t.Indeterminate()
	}
	out := t.Artifact(outDir, 0)
	// A JPEG frame is written as PNG and encoded by the image engine, so
	// transparency gets the same background handling as any other image.
	// A PNG frame keeps FFmpeg's file, with its color and aspect tags.
	viaPNG := to == "jpeg"
	frame := filepath.Join(outDir, "frame.png")
	written, argsFor := out, to
	if viaPNG {
		written, argsFor = frame, "png"
	}
	args := []string{"-hide_banner", "-nostdin", "-y", "-v", "error"}
	args = append(args, localInputArgs...)
	args = append(args, "-progress", "pipe:1", "-nostats", "-i", localPath(input))
	args = append(args, outputArgs(argsFor, t.Options)...)
	args = append(args, written)
	err = runTool(t, "ffmpeg", exec.Command(e.ffmpeg, args...), func(line string) {
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok && total > 0 {
			if us, err := strconv.ParseFloat(v, 64); err == nil {
				t.Progress(us / total)
			}
		}
	})
	if err != nil {
		return nil, noAudioError(err, to)
	}
	switch {
	case viaPNG:
		data, err := os.ReadFile(frame)
		if err != nil {
			return nil, err
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, core.EngineFailed("ffmpeg", "%v", err)
		}
		os.Remove(frame)
		if err := encodeImage(working(img), to, t.Options, out, pngPixelAspect(data)); err != nil {
			return nil, err
		}
	case to == "png":
		if err := backgroundPNG(out, t.Options); err != nil {
			return nil, err
		}
	}
	return []string{out}, nil
}

// pngPixelAspect reads the pixel aspect ratio FFmpeg writes in a frame's
// pHYs chunk, reduced and fitted to JFIF's 16-bit density fields.
func pngPixelAspect(data []byte) *pixelAspect {
	phys := pngChunk(data, "pHYs")
	if len(phys) != 9 {
		return nil
	}
	num, den := binary.BigEndian.Uint32(phys), binary.BigEndian.Uint32(phys[4:])
	if num == 0 || den == 0 {
		return nil
	}
	a, b := num, den
	for b != 0 {
		a, b = b, a%b
	}
	num, den = num/a, den/a
	scale := math.Max(float64(max(num, den)), math.MaxUint16)
	fitted := func(v uint32) uint16 {
		return uint16(math.Max(math.Round(float64(v)*math.MaxUint16/scale), 1))
	}
	return &pixelAspect{fitted(num), fitted(den)}
}

// decodeWithFFmpeg decodes a still image FFmpeg understands (EXR variants
// the built-in decoder doesn't) to 16-bit RGBA.
func decodeWithFFmpeg(t *core.Task, data []byte, format string) (image.Image, error) {
	ffmpeg := FFmpegPath()
	if ffmpeg == "" {
		return nil, fmt.Errorf("ffmpeg not found")
	}
	ctx := context.Background()
	if t != nil {
		ctx = t.Context
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-nostdin", "-v", "error", "-f", format+"_pipe", "-i", "pipe:0",
		"-frames:v", "1", "-pix_fmt", "rgba64be", "-f", "image2pipe", "-c:v", "png", "pipe:1")
	prepareCommand(cmd)
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return png.Decode(&out)
}
