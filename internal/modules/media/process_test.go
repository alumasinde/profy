package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withExif inserts an APP1 Exif segment carrying orientation and a fake GPS tag pointer.
func withExif(jpg []byte, orientation uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	binary.Write(&tiff, binary.BigEndian, uint16(42))
	binary.Write(&tiff, binary.BigEndian, uint32(8))
	binary.Write(&tiff, binary.BigEndian, uint16(2)) // 2 entries
	// Orientation
	binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	binary.Write(&tiff, binary.BigEndian, uint16(3))
	binary.Write(&tiff, binary.BigEndian, uint32(1))
	binary.Write(&tiff, binary.BigEndian, orientation)
	binary.Write(&tiff, binary.BigEndian, uint16(0))
	// GPSInfo IFD pointer (value irrelevant: it must not survive re-encoding)
	binary.Write(&tiff, binary.BigEndian, uint16(0x8825))
	binary.Write(&tiff, binary.BigEndian, uint16(4))
	binary.Write(&tiff, binary.BigEndian, uint32(1))
	binary.Write(&tiff, binary.BigEndian, uint32(0x47505321))
	binary.Write(&tiff, binary.BigEndian, uint32(0))
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(seg)+2))
	out := append([]byte{}, jpg[:2]...)
	out = append(out, app1...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func TestProcessImageVariantsAndNoUpscale(t *testing.T) {
	p, err := ProcessImage(testJPEG(t, 1000, 500))
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 1000 || p.Height != 500 {
		t.Fatalf("dims %dx%d", p.Width, p.Height)
	}
	for _, w := range []int{160, 480, 960, 1000} {
		if p.Variants[w] == nil {
			t.Errorf("missing variant %d (have %v)", w, keys(p.Variants))
		}
	}
	if p.Variants[1600] != nil {
		t.Error("must not upscale to 1600")
	}
	cfg, _ := jpeg.DecodeConfig(bytes.NewReader(p.Variants[480]))
	if cfg.Width != 480 || cfg.Height != 240 {
		t.Errorf("variant 480 is %dx%d", cfg.Width, cfg.Height)
	}
}

func keys(m map[int][]byte) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestExifOrientationAppliedAndMetadataStripped(t *testing.T) {
	src := withExif(testJPEG(t, 400, 200), 6) // rotate 90° clockwise
	if got := jpegOrientation(src); got != 6 {
		t.Fatalf("orientation parsed as %d", got)
	}
	p, err := ProcessImage(src)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 200 || p.Height != 400 {
		t.Fatalf("orientation not applied: %dx%d", p.Width, p.Height)
	}
	for w, b := range p.Variants {
		if bytes.Contains(b, []byte("Exif")) || jpegOrientation(b) != 1 {
			t.Errorf("variant %d still carries EXIF", w)
		}
	}
}

func TestRejectsNonImagesBombsAndTinyImages(t *testing.T) {
	if _, err := ProcessImage([]byte("<html><script>alert(1)</script></html>")); err != ErrUnsupportedType {
		t.Errorf("html: %v", err)
	}
	if _, err := ProcessImage(testJPEG(t, 20, 20)); err != ErrDimensions {
		t.Errorf("tiny: %v", err)
	}
	// A PNG header claiming 20000×20000 must be refused before decoding.
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	bomb := b.Bytes()
	binary.BigEndian.PutUint32(bomb[16:20], 20000)
	binary.BigEndian.PutUint32(bomb[20:24], 20000)
	if _, err := ProcessImage(bomb); err != ErrDimensions && err != ErrCorrupt {
		t.Errorf("bomb: %v", err)
	}
	// A GIF is a valid image but not an accepted type.
	if _, err := ProcessImage([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")); err != ErrUnsupportedType {
		t.Errorf("gif: %v", err)
	}
}

func TestCheckPDF(t *testing.T) {
	ok := []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << >>\n%%EOF\n")
	if err := CheckPDF(ok); err != nil {
		t.Fatalf("plain pdf: %v", err)
	}
	js := []byte("%PDF-1.4\n1 0 obj << /OpenAction << /S /JavaScript /JS (app.alert(1)) >> >> endobj\n%%EOF\n")
	if err := CheckPDF(js); err != ErrUnsafePDF {
		t.Fatalf("js pdf: %v", err)
	}
	if err := CheckPDF([]byte("not a pdf at all")); err != ErrUnsupportedType {
		t.Fatalf("fake: %v", err)
	}
}
