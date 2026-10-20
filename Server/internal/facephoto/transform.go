// Package facephoto owns the lifecycle of per-account face photos.
package facephoto

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"math"
	mathrand "math/rand/v2"

	xdraw "golang.org/x/image/draw"
)

const (
	// DefaultQuality is the JPEG quality used before any size-driven degradation.
	DefaultQuality = 85
	// MinQuality and MaxQuality bound the configurable re-encode quality.
	MinQuality = 80
	MaxQuality = 90
	// MaxUploadBytes is the byte ceiling enforced on uploaded/transformed
	// photos.
	MaxUploadBytes = 100 * 1024
	// MaxBaseBytes bounds a stored base image. Official originals are kept
	// untouched (OMP-52), so this ceiling is above MaxUploadBytes.
	MaxBaseBytes = 8 << 20
	// MaxSourcePixels bounds the decoded dimensions of a base image so a
	// malformed header cannot force a huge allocation during a sign. 16MP
	// covers a 4000x4000 source; Transform downscales to 720x1280 afterwards.
	MaxSourcePixels = 16_000_000
	// MaxWidth and MaxHeight match the manual upload limits enforced by the
	// face upload handler.
	MaxWidth  = 720
	MaxHeight = 1280

	minCropRatio = 0.90
	maxCropRatio = 0.99
	maxAngleDeg  = 5.0

	// safeMargin removes boundary pixels from the rotated safe rectangle so
	// interpolation can never pull in samples from outside the source.
	safeMargin = 1
)

// RandSource is the injectable randomness used for crop ratios, crop offsets
// and rotation angle. *math/rand/v2.Rand satisfies it; tests inject fixed
// seeds so the mandatory transformation is deterministic and assertable.
type RandSource interface {
	Float64() float64
	IntN(n int) int
}

// Options configures Transform. Zero values select production defaults.
type Options struct {
	// Rand is the randomness source. Nil selects a fresh time-seeded source.
	Rand RandSource
	// Quality is the JPEG quality (80-90). Zero selects DefaultQuality.
	Quality int
	// MaxBytes is the output byte ceiling. Zero selects MaxUploadBytes.
	MaxBytes int
}

// ValidateSource checks that src is a decodable JPEG or PNG no larger than
// MaxBaseBytes. It reads only image dimensions and never rewrites the bytes:
// an imported official original is stored exactly as downloaded.
func ValidateSource(src []byte) error {
	if len(src) == 0 {
		return errors.New("face photo base is empty")
	}
	if len(src) > MaxBaseBytes {
		return fmt.Errorf("face photo base is %d bytes, over the %d byte limit", len(src), MaxBaseBytes)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return fmt.Errorf("decode face photo base: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxSourcePixels {
		return fmt.Errorf("face photo base is %dx%d, over the %d pixel limit", cfg.Width, cfg.Height, MaxSourcePixels)
	}
	return nil
}

// Result describes the transformed photo. JPEG is the only bytes a caller may
// upload; it is never persisted (the base image stays in the pool).
type Result struct {
	JPEG            []byte
	Width           int
	Height          int
	CropWidthRatio  float64
	CropHeightRatio float64
	AngleDegrees    float64
	Quality         int
}

// Transform decodes src in memory, crops it, rotates it by a random angle,
// crops the rotated safe rectangle to remove any uncovered border, then
// re-encodes it as JPEG at a bounded size. The re-encode drops all source
// metadata (EXIF included) and the cropping/rotation/quality ladder are
// mandatory: there is no option that returns src unchanged.
func Transform(src []byte, opts Options) (Result, error) {
	rnd := opts.Rand
	if rnd == nil {
		seed := uint64(mathrand.Uint64())
		rnd = mathrand.New(mathrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	}
	quality := opts.Quality
	if quality == 0 {
		quality = DefaultQuality
	}
	if quality < MinQuality || quality > MaxQuality {
		return Result{}, fmt.Errorf("face photo quality %d out of range [%d,%d]", quality, MinQuality, MaxQuality)
	}
	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = MaxUploadBytes
	}
	if maxBytes < 0 {
		return Result{}, errors.New("face photo max bytes must not be negative")
	}

	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return Result{}, fmt.Errorf("decode face photo: %w", err)
	}
	base := toNRGBA(img)
	if base.Bounds().Dx() > MaxWidth || base.Bounds().Dy() > MaxHeight {
		base = fitWithin(base, MaxWidth, MaxHeight)
	}

	w, h := base.Bounds().Dx(), base.Bounds().Dy()
	cropWidthRatio := minCropRatio + rnd.Float64()*(maxCropRatio-minCropRatio)
	cropHeightRatio := minCropRatio + rnd.Float64()*(maxCropRatio-minCropRatio)
	cropW := clampInt(int(math.Round(cropWidthRatio*float64(w))), 1, w)
	cropH := clampInt(int(math.Round(cropHeightRatio*float64(h))), 1, h)
	offsetX := rnd.IntN(w - cropW + 1)
	offsetY := rnd.IntN(h - cropH + 1)
	cropped := image.NewNRGBA(image.Rect(0, 0, cropW, cropH))
	draw.Draw(cropped, cropped.Bounds(), base, image.Pt(offsetX, offsetY), draw.Src)

	// Uniform in [-maxAngleDeg, +maxAngleDeg).
	angleDegrees := (rnd.Float64()*2 - 1) * maxAngleDeg
	rotated := rotateSafeRect(cropped, angleDegrees*math.Pi/180)

	jpegBytes, usedQuality, final, err := encodeUnderCap(rotated, quality, maxBytes)
	if err != nil {
		return Result{}, err
	}

	return Result{
		JPEG:            jpegBytes,
		Width:           final.Bounds().Dx(),
		Height:          final.Bounds().Dy(),
		CropWidthRatio:  float64(cropW) / float64(w),
		CropHeightRatio: float64(cropH) / float64(h),
		AngleDegrees:    angleDegrees,
		Quality:         usedQuality,
	}, nil
}

// rotateSafeRect rotates src by theta and returns the largest axis-aligned
// rectangle of the same aspect ratio that stays entirely inside src. Sampling
// clamps to the source bounds, so the result never contains uncovered borders.
func rotateSafeRect(src *image.NRGBA, theta float64) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	cosT, sinT := math.Cos(theta), math.Sin(theta)
	absCos, absSin := math.Abs(cosT), math.Abs(sinT)

	keep := math.Min(
		float64(w)/(float64(w)*absCos+float64(h)*absSin),
		float64(h)/(float64(w)*absSin+float64(h)*absCos),
	)
	outW := clampInt(int(math.Floor(keep*float64(w)))-2*safeMargin, 1, w)
	outH := clampInt(int(math.Floor(keep*float64(h)))-2*safeMargin, 1, h)

	dst := image.NewNRGBA(image.Rect(0, 0, outW, outH))
	startX := (float64(w) - float64(outW)) / 2
	startY := (float64(h) - float64(outH)) / 2
	cx, cy := float64(w)/2, float64(h)/2

	for y := 0; y < outH; y++ {
		dy := startY + float64(y) + 0.5 - cy
		for x := 0; x < outW; x++ {
			dx := startX + float64(x) + 0.5 - cx
			// Inverse rotation: destination pixel -> source canvas.
			sx := cx + cosT*dx + sinT*dy
			sy := cy - sinT*dx + cosT*dy
			dst.SetNRGBA(x, y, sampleBilinear(src, sx-0.5, sy-0.5))
		}
	}
	return dst
}

// sampleBilinear samples m at pixel-index coordinates with edge clamping.
func sampleBilinear(m *image.NRGBA, x, y float64) color.NRGBA {
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	x = math.Min(math.Max(x, 0), float64(w-1))
	y = math.Min(math.Max(y, 0), float64(h-1))

	x0, y0 := int(x), int(y)
	x1, y1 := clampInt(x0+1, 0, w-1), clampInt(y0+1, 0, h-1)
	fx, fy := x-float64(x0), y-float64(y0)

	c00, c10 := m.NRGBAAt(x0, y0), m.NRGBAAt(x1, y0)
	c01, c11 := m.NRGBAAt(x0, y1), m.NRGBAAt(x1, y1)

	blend := func(a, b, c, d uint8) uint8 {
		top := (1-fx)*float64(a) + fx*float64(b)
		bottom := (1-fx)*float64(c) + fx*float64(d)
		return uint8(math.Round((1-fy)*top + fy*bottom))
	}
	return color.NRGBA{
		R: blend(c00.R, c10.R, c01.R, c11.R),
		G: blend(c00.G, c10.G, c01.G, c11.G),
		B: blend(c00.B, c10.B, c01.B, c11.B),
		A: 255,
	}
}

// encodeUnderCap applies the mandated 85 -> 80 -> 70 quality ladder, then a
// bounded downscale fallback, returning output no larger than maxBytes.
func encodeUnderCap(img *image.NRGBA, firstQuality, maxBytes int) ([]byte, int, *image.NRGBA, error) {
	ladder := []int{firstQuality}
	for _, q := range []int{80, 70} {
		if q < firstQuality {
			ladder = append(ladder, q)
		}
	}

	for _, q := range ladder {
		buf, err := encodeJPEG(img, q)
		if err != nil {
			return nil, 0, nil, err
		}
		if len(buf) <= maxBytes {
			return buf, q, img, nil
		}
	}

	// The quality floor was not enough; reduce dimensions at the floor quality.
	lowest := ladder[len(ladder)-1]
	cur := img
	for attempt := 0; attempt < 8; attempt++ {
		if cur.Bounds().Dx() <= 16 || cur.Bounds().Dy() <= 16 {
			break
		}
		cur = fitWithin(cur, int(math.Round(float64(cur.Bounds().Dx())*0.8)), int(math.Round(float64(cur.Bounds().Dy())*0.8)))
		buf, err := encodeJPEG(cur, lowest)
		if err != nil {
			return nil, 0, nil, err
		}
		if len(buf) <= maxBytes {
			return buf, lowest, cur, nil
		}
	}
	return nil, 0, nil, fmt.Errorf("face photo cannot be compressed to %d bytes", maxBytes)
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode face photo: %w", err)
	}
	return buf.Bytes(), nil
}

// toNRGBA normalizes any decoded image to non-premultiplied RGBA.
func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok && n.Bounds().Min == (image.Point{}) {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// fitWithin scales src down to fit maxW x maxH, preserving aspect ratio.
func fitWithin(src *image.NRGBA, maxW, maxH int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := math.Min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	if scale >= 1 {
		return src
	}
	nw := clampInt(int(math.Round(float64(w)*scale)), 1, maxW)
	nh := clampInt(int(math.Round(float64(h)*scale)), 1, maxH)
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
