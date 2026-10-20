// Package captcha locates the slide-gap x offset of a 学习通 slider captcha.
//
// The inference path mirrors ChaoxingSignFaker's ChaoxingCaptchaPredictor
// (app/src/main/java/org/aquamarine5/brainspark/chaoxingsignfaker/api/
// ChaoxingCaptchaPredictor.kt:22-60): a YOLO11n single-class detector takes the
// shade (background) image, padded to a square and scaled to 640x640, and
// predicts [cx, cy, w, h, score] boxes. The top box after score filtering and
// NMS determines the gap.
//
// Building this package requires cgo. When cgo is disabled, New reports
// ErrRuntimeUnavailable so callers can keep using the heuristic path.
package captcha

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/draw"
)

const (
	// ModelInputSize is the detector input side length.
	// Source: ChaoxingCaptchaPredictor.kt MODEL_INPUT_SIZE.
	ModelInputSize = 640
	// ScoreThreshold drops low-confidence boxes before NMS.
	// Source: ChaoxingCaptchaPredictor.kt SCORE_THRESHOLD.
	ScoreThreshold = 0.25
	// NMSIoUThreshold suppresses overlapping boxes.
	// Source: ChaoxingCaptchaPredictor.kt NMS_IOU_THRESHOLD.
	NMSIoUThreshold = 0.45
	// EdgeOffset compensates for the detector's box edge bias.
	// Source: ChaoxingCaptchaPredictor.kt EDGE_OFFSET.
	EdgeOffset = 4

	// modelInputName / modelOutputName are embedded in captcha.ort.
	modelInputName  = "images"
	modelOutputName = "output0"
	// modelAttributes is the YOLO output channel count: cx, cy, w, h, score.
	modelAttributes = 5
	// modelAnchors is the YOLO11n anchor count for a 640x640 input.
	modelAnchors = 8400
)

// ErrRuntimeUnavailable means the binary was built without cgo, so ONNX
// Runtime cannot be called.
var ErrRuntimeUnavailable = errors.New("captcha: ONNX Runtime support requires cgo")

// Predictor runs captcha.ort. It is safe for concurrent use; inference is
// serialized because one ONNX session cannot run concurrently.
type Predictor struct {
	path     string
	mu       sync.Mutex
	detector detector
}

// detector isolates the ONNX Runtime implementation so non-cgo builds can
// still compile and fall back to the heuristic.
type detector interface {
	Run(input []float32) ([]float32, error)
	Close() error
}

// New loads the ONNX model at modelPath and initializes ONNX Runtime.
//
// The shared library is located through CAPTCHA_ORT_LIB_PATH. When unset, the
// platform default "libonnxruntime.so" is resolved via the loader path
// (LD_LIBRARY_PATH or ldconfig). The runtime version must match the C API
// headers used by onnxruntime_go.
func New(modelPath string) (*Predictor, error) {
	modelPath = strings.TrimSpace(modelPath)
	if modelPath == "" {
		return nil, errors.New("captcha: model path is empty")
	}
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("captcha: model file unavailable: %w", err)
	}
	det, err := newDetector(modelPath)
	if err != nil {
		return nil, err
	}
	return &Predictor{path: modelPath, detector: det}, nil
}

// Close releases the ONNX session and tensors. It does not destroy the shared
// ONNX Runtime environment, which may still be used by other predictors.
func (p *Predictor) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.detector == nil {
		return nil
	}
	err := p.detector.Close()
	p.detector = nil
	return err
}

// PredictSlideX returns the left edge x of the highest-confidence slide gap in
// the shade (background) image. The result is in the original image's pixel
// space, using the same edge offset as CSF.
func (p *Predictor) PredictSlideX(img []byte) (int, error) {
	if p == nil {
		return 0, errors.New("captcha: predictor is not initialized")
	}
	decoded, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return 0, fmt.Errorf("captcha: decode image: %w", err)
	}
	input, paddedLength, err := preprocess(decoded)
	if err != nil {
		return 0, err
	}

	p.mu.Lock()
	if p.detector == nil {
		p.mu.Unlock()
		return 0, errors.New("captcha: predictor is closed")
	}
	output, err := p.detector.Run(input)
	p.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("captcha: inference: %w", err)
	}
	return decodePredictions(output, paddedLength)
}

// preprocess pads the image to a square with black and scales it to
// ModelInputSize, matching CSF's createBitmap(max(w,h)) + drawBitmap(0,0) +
// scale(640,640). It returns planar NCHW float32 values in [0,1].
func preprocess(img image.Image) ([]float32, int, error) {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 {
		return nil, 0, errors.New("captcha: invalid image size")
	}

	paddedLength := width
	if height > paddedLength {
		paddedLength = height
	}
	padded := image.NewRGBA(image.Rect(0, 0, paddedLength, paddedLength))
	draw.Draw(padded, image.Rect(0, 0, width, height), img, b.Min, draw.Src)

	scaled := image.NewRGBA(image.Rect(0, 0, ModelInputSize, ModelInputSize))
	draw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), padded, padded.Bounds(), draw.Src, nil)

	input := make([]float32, 3*ModelInputSize*ModelInputSize)
	plane := ModelInputSize * ModelInputSize
	pix := scaled.Pix
	for i, j := 0, 0; i < len(pix); i, j = i+4, j+1 {
		input[j] = float32(pix[i]) / 255
		input[plane+j] = float32(pix[i+1]) / 255
		input[2*plane+j] = float32(pix[i+2]) / 255
	}
	return input, paddedLength, nil
}

type prediction struct {
	x, y, width, height, score float32
}

// decodePredictions applies score threshold, NMS and the CSF edge offset to a
// [1,5,8400] YOLO output.
func decodePredictions(output []float32, paddedLength int) (int, error) {
	if len(output) != modelAttributes*modelAnchors {
		return 0, fmt.Errorf("captcha: unexpected model output length %d", len(output))
	}
	if paddedLength <= 0 {
		return 0, errors.New("captcha: invalid padded length")
	}

	boxes := make([]prediction, 0, modelAnchors)
	for i := 0; i < modelAnchors; i++ {
		score := output[(modelAttributes-1)*modelAnchors+i]
		if score < ScoreThreshold {
			continue
		}
		cx := output[i]
		cy := output[modelAnchors+i]
		w := output[2*modelAnchors+i]
		h := output[3*modelAnchors+i]
		boxes = append(boxes, prediction{
			x:      cx - w/2,
			y:      cy - h/2,
			width:  w,
			height: h,
			score:  score,
		})
	}
	if len(boxes) == 0 {
		return 0, errors.New("captcha: no slide gap detected above score threshold")
	}

	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].score > boxes[j].score })
	kept := boxes[:0]
	for _, candidate := range boxes {
		suppressed := false
		for _, selected := range kept {
			if iou(candidate, selected) > NMSIoUThreshold {
				suppressed = true
				break
			}
		}
		if !suppressed {
			kept = append(kept, candidate)
		}
	}

	// CSF: (best.x * paddedLength / 640 - EDGE_OFFSET + 0.5f).toInt().
	best := kept[0]
	x := int(best.x*float32(paddedLength)/ModelInputSize - EdgeOffset + 0.5)
	if float32(x) < 0 {
		return 0, errors.New("captcha: predicted x is negative")
	}
	return x, nil
}

func iou(a, b prediction) float32 {
	intersectWidth := min32(a.x+a.width, b.x+b.width) - max32(a.x, b.x)
	intersectHeight := min32(a.y+a.height, b.y+b.height) - max32(a.y, b.y)
	if intersectWidth <= 0 || intersectHeight <= 0 {
		return 0
	}
	intersection := intersectWidth * intersectHeight
	union := a.width*a.height + b.width*b.height - intersection
	if union <= 0 {
		return 0
	}
	return intersection / union
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
