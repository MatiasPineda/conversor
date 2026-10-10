package engines

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

// PDFEngine renders PDF pages with PDFium, the renderer in Chrome, compiled
// to WebAssembly and embedded in the binary. It writes one image per
// rendered page; the registry names them doc.png, doc-2.png and so on.
type PDFEngine struct{}

func NewPDFEngine() *PDFEngine { return &PDFEngine{} }

func (*PDFEngine) ID() string          { return "pdfium" }
func (*PDFEngine) Priority() int       { return 10 }
func (*PDFEngine) Unavailable() string { return "" }
func (*PDFEngine) Steps() []core.Step  { return core.Steps([]string{"pdf"}, []string{"png", "jpeg"}) }

var (
	pdfiumOnce sync.Once
	pdfiumPool pdfium.Pool
	pdfiumErr  error
)

// pdfiumInstances starts the WebAssembly runtime on first use. Compiled
// code is cached on disk, so later runs start faster.
func pdfiumInstances() (pdfium.Pool, error) {
	pdfiumOnce.Do(func() {
		cfg := wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling)
		if dir := CacheDir(); dir != "" {
			if cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(dir, "wazero")); err == nil {
				cfg = cfg.WithCompilationCache(cache)
			}
		}
		pdfiumPool, pdfiumErr = webassembly.Init(webassembly.Config{
			MinIdle:  0,
			MaxIdle:  1,
			MaxTotal: runtime.NumCPU(),
			// Documents are passed as bytes, so PDFium needs no filesystem.
			FSConfig:      wazero.NewFSConfig(),
			RuntimeConfig: cfg,
		})
	})
	return pdfiumPool, pdfiumErr
}

func pdfFailed(err error) error { return core.EngineFailed("pdfium", "%v", err) }

func (*PDFEngine) Convert(t *core.Task, input, outDir string) ([]string, error) {
	o := t.Options
	to := t.Step.To
	// Pages render onto the chosen background. Transparent keeps the page
	// background clear where the output can store it; everything else,
	// and the default, is white, which is what PDF viewers show.
	fill := uint64(0xffffffff)
	if b := o.Background; b != nil {
		switch {
		case b.Transparent && !to.KeepsTransparency():
			return nil, core.InvalidOption("%s", noTransparency(to.ID))
		case b.Transparent:
			fill = 0x00ffffff
		default:
			fill = 0xff000000 | uint64(b.Color[0])<<16 | uint64(b.Color[1])<<8 | uint64(b.Color[2])
		}
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	t.Indeterminate()
	pool, err := pdfiumInstances()
	if err != nil {
		return nil, pdfFailed(err)
	}
	inst, err := pool.GetInstanceWithContext(t.Context)
	if err != nil {
		if t.Cancelled() {
			return nil, core.ErrCancelled
		}
		return nil, pdfFailed(err)
	}
	defer inst.Close()
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return nil, pdfFailed(err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	countRes, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, pdfFailed(err)
	}
	count := countRes.PageCount
	var wanted []int
	for i := range count {
		if o.Pages == nil || o.Pages.Contains(i) {
			wanted = append(wanted, i)
		}
	}
	if len(wanted) == 0 {
		asked := i18n.T("any")
		if o.Pages != nil {
			asked = o.Pages.String()
		}
		return nil, core.InvalidOption("%s", i18n.N(count, "the document has %d page, so pages %s select nothing", "the document has %d pages, so pages %s select nothing", asked))
	}
	var files []string
	for n, i := range wanted {
		if err := t.Check(); err != nil {
			return nil, err
		}
		img, err := renderPDFPage(inst, doc.Document, i, o, fill)
		if err != nil {
			return nil, err
		}
		// Named by page number, so outputs are named after the pages they
		// came from when --pages skips some.
		path := t.Artifact(outDir, i)
		if err := encodeImage(img, to.ID, o, path, nil); err != nil {
			return nil, err
		}
		files = append(files, path)
		t.Progress(float64(n+1) / float64(len(wanted)))
	}
	return files, nil
}

// pageSize is the pixel size for a page of w×h points: 2000 pixels wide and
// at most 4000 tall by default, or the DPI asked for, then capped by
// MaxSize, keeping the aspect ratio.
func pageSize(w, h float64, o *core.Options) (int, int) {
	var scale float64
	if o.DPI != nil {
		scale = float64(*o.DPI) / 72
	} else {
		scale = 2000 / w
		if h*scale > 4000 {
			scale = 4000 / h
		}
	}
	if o.MaxSize != nil {
		if longest := math.Max(w, h) * scale; longest > float64(*o.MaxSize) {
			scale *= float64(*o.MaxSize) / longest
		}
	}
	return max(1, int(math.Round(w*scale))), max(1, int(math.Round(h*scale)))
}

func renderPDFPage(inst pdfium.Pdfium, doc references.FPDF_DOCUMENT, index int, o *core.Options, fill uint64) (image.Image, error) {
	page, err := inst.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: doc, Index: index})
	if err != nil {
		return nil, pdfFailed(err)
	}
	defer inst.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: page.Page})
	ref := requests.Page{ByReference: &page.Page}
	wRes, err := inst.FPDF_GetPageWidthF(&requests.FPDF_GetPageWidthF{Page: ref})
	if err != nil {
		return nil, pdfFailed(err)
	}
	hRes, err := inst.FPDF_GetPageHeightF(&requests.FPDF_GetPageHeightF{Page: ref})
	if err != nil {
		return nil, pdfFailed(err)
	}
	w, h := pageSize(float64(wRes.PageWidth), float64(hRes.PageHeight), o)
	if w*h > 1<<28 {
		return nil, core.InvalidOption("%s", i18n.T("a %d×%d pixel page is too large; lower the DPI", w, h))
	}
	bmp, err := inst.FPDFBitmap_Create(&requests.FPDFBitmap_Create{Width: w, Height: h, Alpha: 1})
	if err != nil {
		return nil, pdfFailed(err)
	}
	defer inst.FPDFBitmap_Destroy(&requests.FPDFBitmap_Destroy{Bitmap: bmp.Bitmap})
	if _, err := inst.FPDFBitmap_FillRect(&requests.FPDFBitmap_FillRect{Bitmap: bmp.Bitmap, Width: w, Height: h, Color: fill}); err != nil {
		return nil, pdfFailed(err)
	}
	if _, err := inst.FPDF_RenderPageBitmap(&requests.FPDF_RenderPageBitmap{
		Bitmap: bmp.Bitmap, Page: ref, SizeX: w, SizeY: h,
		Flags: enums.FPDF_RENDER_FLAG_ANNOT | enums.FPDF_RENDER_FLAG_REVERSE_BYTE_ORDER,
	}); err != nil {
		return nil, pdfFailed(err)
	}
	stride, err := inst.FPDFBitmap_GetStride(&requests.FPDFBitmap_GetStride{Bitmap: bmp.Bitmap})
	if err != nil {
		return nil, pdfFailed(err)
	}
	buf, err := inst.FPDFBitmap_GetBuffer(&requests.FPDFBitmap_GetBuffer{Bitmap: bmp.Bitmap})
	if err != nil {
		return nil, pdfFailed(err)
	}
	// PDFium writes straight (not premultiplied) alpha, RGBA with the
	// reversed byte order flag.
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		copy(img.Pix[y*img.Stride:][:4*w], buf.Buffer[y*stride.Stride:])
	}
	return img, nil
}
