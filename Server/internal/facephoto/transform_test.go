package facephoto_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"testing"

	"xbt2/server/internal/facephoto"
)

const maxBytes = 100 * 1024

func TestTransformAppliesMandatoryGeometry(t *testing.T) {
	src := withEXIF(encodeJPEG(t, gradientImage(640, 480), 92))
	if !hasEXIF(src) {
		t.Fatal("test precondition: source must carry an EXIF APP1 segment")
	}

	var angles []float64
	for _, seed := range []uint64{1, 42, 2026} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rnd := rand.New(rand.NewPCG(seed, seed+1))
			res, err := facephoto.Transform(src, facephoto.Options{Rand: rnd})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}

			if res.CropWidthRatio < 0.90 || res.CropWidthRatio > 0.99 {
				t.Fatalf("crop width ratio %v outside [0.90,0.99]", res.CropWidthRatio)
			}
			if res.CropHeightRatio < 0.90 || res.CropHeightRatio > 0.99 {
				t.Fatalf("crop height ratio %v outside [0.90,0.99]", res.CropHeightRatio)
			}
			if math.Abs(res.AngleDegrees) > 5 {
				t.Fatalf("angle %v outside [-5,5]", res.AngleDegrees)
			}
			if bytes.Equal(res.JPEG, src) {
				t.Fatal("output bytes equal input: transform was not applied")
			}
			if hasEXIF(res.JPEG) {
				t.Fatal("output still contains EXIF metadata")
			}
			assertNoBlackBorder(t, res.JPEG, 120)

			angles = append(angles, res.AngleDegrees)
		})
	}

	if angles[0] == angles[1] && angles[1] == angles[2] {
		t.Fatalf("angles %v did not vary across seeds: injected randomness ignored", angles)
	}
}

func TestTransformIsDeterministicForSameSeed(t *testing.T) {
	src := encodeJPEG(t, gradientImage(400, 400), 90)

	first, err := facephoto.Transform(src, facephoto.Options{Rand: rand.New(rand.NewPCG(7, 11))})
	if err != nil {
		t.Fatalf("first Transform: %v", err)
	}
	second, err := facephoto.Transform(src, facephoto.Options{Rand: rand.New(rand.NewPCG(7, 11))})
	if err != nil {
		t.Fatalf("second Transform: %v", err)
	}
	if !bytes.Equal(first.JPEG, second.JPEG) {
		t.Fatal("same seed produced different output bytes")
	}
}

func TestTransformHonorsSizeCap(t *testing.T) {
	src := encodeJPEG(t, noiseImage(720, 1280, 99), 95)
	if len(src) <= 150*1024 {
		t.Fatalf("test precondition: source must exceed the cap, got %d bytes", len(src))
	}

	res, err := facephoto.Transform(src, facephoto.Options{Rand: rand.New(rand.NewPCG(5, 6))})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(res.JPEG) > maxBytes {
		t.Fatalf("output %d bytes exceeds cap %d", len(res.JPEG), maxBytes)
	}
	t.Logf("source %d bytes -> output %d bytes, quality %d, %dx%d", len(src), len(res.JPEG), res.Quality, res.Width, res.Height)
	if res.Quality >= facephoto.DefaultQuality {
		t.Fatalf("quality %d did not degrade from default %d", res.Quality, facephoto.DefaultQuality)
	}
	img, err := jpeg.Decode(bytes.NewReader(res.JPEG))
	if err != nil {
		t.Fatalf("output is not a decodable JPEG: %v", err)
	}
	if img.Bounds().Dx() != res.Width || img.Bounds().Dy() != res.Height {
		t.Fatalf("reported size %dx%d does not match decoded %dx%d", res.Width, res.Height, img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestTransformVariesAcrossCallsWithOneSource(t *testing.T) {
	src := encodeJPEG(t, gradientImage(400, 400), 90)
	rnd := rand.New(rand.NewPCG(11, 13))

	first, err := facephoto.Transform(src, facephoto.Options{Rand: rnd})
	if err != nil {
		t.Fatalf("first Transform: %v", err)
	}
	second, err := facephoto.Transform(src, facephoto.Options{Rand: rnd})
	if err != nil {
		t.Fatalf("second Transform: %v", err)
	}
	if first.CropWidthRatio == second.CropWidthRatio &&
		first.CropHeightRatio == second.CropHeightRatio &&
		first.AngleDegrees == second.AngleDegrees {
		t.Fatalf("two calls drew identical geometry: %+v", first)
	}
	if bytes.Equal(first.JPEG, second.JPEG) {
		t.Fatal("two calls from one injected source produced identical bytes")
	}
}

func TestTransformRejectsUndecodableInput(t *testing.T) {
	if _, err := facephoto.Transform([]byte("not an image"), facephoto.Options{}); err == nil {
		t.Fatal("expected an error for undecodable input")
	}
}

func TestValidateSourceAcceptsOriginalAndRejectsGarbage(t *testing.T) {
	src := encodeJPEG(t, gradientImage(320, 240), 90)
	if err := facephoto.ValidateSource(src); err != nil {
		t.Fatalf("ValidateSource(original) = %v", err)
	}
	if err := facephoto.ValidateSource([]byte("not an image")); err == nil {
		t.Fatal("ValidateSource accepted non-image bytes")
	}
	if err := facephoto.ValidateSource(nil); err == nil {
		t.Fatal("ValidateSource accepted empty bytes")
	}
}

func gradientImage(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(140 + 100*x/w),
				G: uint8(150 + 80*y/h),
				B: 170,
				A: 255,
			})
		}
	}
	return img
}

func noiseImage(w, h int, seed uint64) image.Image {
	rnd := rand.New(rand.NewPCG(seed, seed+1))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(rnd.IntN(256)),
				G: uint8(rnd.IntN(256)),
				B: uint8(rnd.IntN(256)),
				A: 255,
			})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode source JPEG: %v", err)
	}
	return buf.Bytes()
}

func assertNoBlackBorder(t *testing.T, jpg []byte, minChannel uint8) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bch, _ := img.At(x, y).RGBA()
			if r>>8 < uint32(minChannel) || g>>8 < uint32(minChannel) || bch>>8 < uint32(minChannel) {
				t.Fatalf("uncovered border pixel at (%d,%d): %d,%d,%d", x, y, r>>8, g>>8, bch>>8)
			}
		}
	}
}

// withEXIF prepends an APP1/Exif segment after SOI.
func withEXIF(jpg []byte) []byte {
	payload := []byte("Exif\x00\x00MM\x00*\x00\x00\x00\x08")
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	out := make([]byte, 0, len(jpg)+len(seg)+len(payload))
	out = append(out, jpg[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	out = append(out, jpg[2:]...)
	return out
}

func hasEXIF(jpg []byte) bool {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return false
	}
	for i := 2; i+4 <= len(jpg); {
		if jpg[i] != 0xFF {
			return false
		}
		marker := jpg[i+1]
		if marker == 0xD9 || marker == 0xDA {
			return false
		}
		length := int(jpg[i+2])<<8 | int(jpg[i+3])
		if length < 2 {
			return false
		}
		if marker == 0xE1 && i+4+6 <= len(jpg) && string(jpg[i+4:i+10]) == "Exif\x00\x00" {
			return true
		}
		i += 2 + length
	}
	return false
}
