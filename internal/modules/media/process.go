package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // registered decoders: content is detected, never trusted from the extension
	"net/http"
	"slices"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Responsive widths generated for public images (§17).
var Widths = []int{160, 480, 960, 1600}

// Limits that guard against decompression bombs and useless images.
const (
	MaxPixels    = 40_000_000
	MaxSide      = 12_000
	MinSide      = 64
	DocMaxWidth  = 2400
	JPEGQuality  = 82
	AbsMaxUpload = 20 << 20 // hard floor regardless of settings
	sniffLen     = 512
	workMaxSide  = 2400 // images are pre-scaled to this before rotation and final resizing
)

// Errors returned to clients as validation codes.
var (
	ErrUnsupportedType = errors.New("unsupported_type")
	ErrTooLarge        = errors.New("too_large")
	ErrDimensions      = errors.New("bad_dimensions")
	ErrCorrupt         = errors.New("corrupt_file")
	ErrUnsafePDF       = errors.New("unsafe_pdf")
)

var imageTypes = []string{"image/jpeg", "image/png", "image/webp"}

// DetectType returns the content type from the bytes themselves (§16.1).
func DetectType(b []byte) string {
	n := len(b)
	if n > sniffLen {
		n = sniffLen
	}
	ct := http.DetectContentType(b[:n])
	if ct == "application/octet-stream" && len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		ct = "image/webp"
	}
	return ct
}

// Processed is a re-encoded image and its responsive variants (JPEG).
type Processed struct {
	Width, Height int
	Variants      map[int][]byte // width → JPEG bytes
}

// widths returns which variant widths apply to an image of width w (never upscaling;
// always at least one variant).
func widths(w int) []int {
	var out []int
	for _, x := range Widths {
		if x <= w {
			out = append(out, x)
		}
	}
	if len(out) == 0 || (out[len(out)-1] < w && out[len(out)-1] < Widths[len(Widths)-1]) {
		out = append(out, w)
	}
	return out
}

// decodeImage validates dimensions before decoding and applies EXIF orientation.
func decodeImage(b []byte) (image.Image, error) {
	ct := DetectType(b)
	if !slices.Contains(imageTypes, ct) {
		return nil, ErrUnsupportedType
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, ErrCorrupt
	}
	if cfg.Width < MinSide || cfg.Height < MinSide || cfg.Width > MaxSide || cfg.Height > MaxSide || cfg.Width*cfg.Height > MaxPixels {
		return nil, ErrDimensions
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, ErrCorrupt
	}
	img = prescale(img)
	if ct == "image/jpeg" {
		img = orient(img, jpegOrientation(b))
	}
	return img, nil
}

// ProcessImage re-encodes an uploaded image as JPEG at the responsive widths. Re-encoding
// drops every metadata block, including EXIF GPS coordinates (§17), and transparent areas
// are flattened onto white.
func ProcessImage(b []byte) (*Processed, error) {
	img, err := decodeImage(b)
	if err != nil {
		return nil, err
	}
	bnd := img.Bounds()
	p := &Processed{Width: bnd.Dx(), Height: bnd.Dy(), Variants: map[int][]byte{}}
	for _, w := range widths(p.Width) {
		out, err := encodeAt(img, w)
		if err != nil {
			return nil, err
		}
		p.Variants[w] = out
	}
	return p, nil
}

// ProcessDocumentImage re-encodes an image document (ID photo) at one size for review.
func ProcessDocumentImage(b []byte) ([]byte, int, int, error) {
	img, err := decodeImage(b)
	if err != nil {
		return nil, 0, 0, err
	}
	w := img.Bounds().Dx()
	if w > DocMaxWidth {
		w = DocMaxWidth
	}
	out, err := encodeAt(img, w)
	if err != nil {
		return nil, 0, 0, err
	}
	h := img.Bounds().Dy() * w / img.Bounds().Dx()
	return out, w, h, nil
}

func encodeAt(img image.Image, w int) ([]byte, error) {
	b := img.Bounds()
	h := b.Dy() * w / b.Dx()
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// prescale cheaply shrinks very large images so later steps stay fast.
func prescale(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := max(w, h)
	if long <= workMaxSide {
		return img
	}
	nw, nh := w*workMaxSide/long, h*workMaxSide/long
	dst := image.NewRGBA(image.Rect(0, 0, max(nw, 1), max(nh, 1)))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// orient applies an EXIF orientation transform.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// CheckPDF accepts only plain PDFs: the magic header, an EOF marker, and none of the
// active-content features (JavaScript, launch actions, embedded files) used by polyglots.
func CheckPDF(b []byte) error {
	if len(b) < 8 || string(b[:5]) != "%PDF-" {
		return ErrUnsupportedType
	}
	if !bytes.Contains(b[max(0, len(b)-2048):], []byte("%%EOF")) {
		return ErrCorrupt
	}
	for _, bad := range [][]byte{[]byte("/JavaScript"), []byte("/JS"), []byte("/Launch"), []byte("/EmbeddedFile"), []byte("/OpenAction"), []byte("/RichMedia"), []byte("/XFA")} {
		if bytes.Contains(b, bad) {
			return ErrUnsafePDF
		}
	}
	return nil
}
