package captcha

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type modelFixture struct {
	file string
	want int
}

// want is the x accepted by the upstream check endpoint for an anonymous
// challenge captured without an account. Images contain no token or account
// data; see testdata/README.md.
var modelFixtures = []modelFixture{
	{file: "slide_01.jpg", want: 168},
	{file: "slide_02.jpg", want: 217},
	{file: "slide_03.jpg", want: 212},
}

func TestNewRejectsMissingModel(t *testing.T) {
	_, err := New(filepath.Join(t.TempDir(), "missing.ort"))
	if err == nil {
		t.Fatal("New(missing) error = nil, want explicit model file error")
	}
	if !strings.Contains(err.Error(), "model file unavailable") {
		t.Fatalf("New(missing) error = %q, want model file unavailable", err)
	}
}

func TestNewRejectsEmptyPath(t *testing.T) {
	_, err := New("  ")
	if err == nil {
		t.Fatal("New(empty) error = nil, want error")
	}
}

func TestDecodePredictionsAppliesThresholdNMSAndEdgeOffset(t *testing.T) {
	output := make([]float32, modelAttributes*modelAnchors)

	setBox := func(index int, cx, cy, w, h, score float32) {
		output[index] = cx
		output[modelAnchors+index] = cy
		output[2*modelAnchors+index] = w
		output[3*modelAnchors+index] = h
		output[4*modelAnchors+index] = score
	}
	setBox(0, 180, 100, 56, 56, 0.90) // x=152 in 640-space
	setBox(1, 182, 101, 56, 56, 0.80) // IoU > 0.45, suppressed
	setBox(2, 400, 100, 56, 56, 0.10) // below threshold

	got, err := decodePredictions(output, 320)
	if err != nil {
		t.Fatalf("decodePredictions error = %v", err)
	}
	// 152 * 320 / 640 - 4 + 0.5 = 72.5 -> 72
	if got != 72 {
		t.Fatalf("decodePredictions = %d, want 72", got)
	}
}

func TestDecodePredictionsNoCandidate(t *testing.T) {
	output := make([]float32, modelAttributes*modelAnchors)
	_, err := decodePredictions(output, 320)
	if err == nil {
		t.Fatal("decodePredictions error = nil, want no candidate error")
	}
}

func TestDecodePredictionsRejectsWrongLength(t *testing.T) {
	_, err := decodePredictions(make([]float32, 10), 320)
	if err == nil {
		t.Fatal("decodePredictions error = nil, want length error")
	}
}

func TestPreprocessPadsToSquare(t *testing.T) {
	img := loadFixtureImage(t, "slide_01.jpg")
	input, paddedLength, err := preprocess(img)
	if err != nil {
		t.Fatalf("preprocess error = %v", err)
	}
	if paddedLength != 320 {
		t.Fatalf("paddedLength = %d, want 320", paddedLength)
	}
	if len(input) != 3*ModelInputSize*ModelInputSize {
		t.Fatalf("input length = %d, want %d", len(input), 3*ModelInputSize*ModelInputSize)
	}
}

func TestPredictSlideXModelFixtures(t *testing.T) {
	modelPath := filepath.Join("..", "..", "assets", "captcha.ort")
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("model asset unavailable: %v", err)
	}
	predictor, err := New(modelPath)
	if err != nil {
		if os.Getenv("CAPTCHA_TEST_REQUIRE_MODEL") == "1" {
			t.Fatalf("New(%s) error = %v", modelPath, err)
		}
		t.Skipf("ONNX Runtime unavailable: %v", err)
	}
	t.Cleanup(func() { _ = predictor.Close() })

	for _, fixture := range modelFixtures {
		fixture := fixture
		t.Run(fixture.file, func(t *testing.T) {
			img := loadFixtureBytes(t, fixture.file)
			got, err := predictor.PredictSlideX(img)
			if err != nil {
				t.Fatalf("PredictSlideX error = %v", err)
			}
			if diff := got - fixture.want; diff < -2 || diff > 2 {
				t.Fatalf("PredictSlideX = %d, want %d +/- 2", got, fixture.want)
			}
		})
	}
}

func loadFixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func loadFixtureImage(t *testing.T, name string) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(loadFixtureBytes(t, name)))
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return img
}
