//go:build cgo

package captcha

import (
	"errors"
	"fmt"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	// EnvRuntimeLibrary overrides the ONNX Runtime shared library path.
	EnvRuntimeLibrary = "CAPTCHA_ORT_LIB_PATH"
	// DefaultRuntimeLibrary is resolved through the dynamic loader when the
	// override is unset.
	DefaultRuntimeLibrary = "libonnxruntime.so"
)

var (
	runtimeOnce sync.Mutex
	runtimeErr  error
)

// initRuntime loads and initializes the ONNX Runtime shared library exactly
// once per process. onnxruntime_go keeps process-global state.
func initRuntime() error {
	runtimeOnce.Lock()
	defer runtimeOnce.Unlock()
	if runtimeErr != nil {
		return runtimeErr
	}
	if ort.IsInitialized() {
		return nil
	}
	lib := os.Getenv(EnvRuntimeLibrary)
	if lib == "" {
		lib = DefaultRuntimeLibrary
	}
	ort.SetSharedLibraryPath(lib)
	if err := ort.InitializeEnvironment(); err != nil {
		runtimeErr = fmt.Errorf("captcha: initialize ONNX Runtime (%s): %w", lib, err)
		return runtimeErr
	}
	return nil
}

type onnxDetector struct {
	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
}

func newDetector(modelPath string) (detector, error) {
	if err := initRuntime(); err != nil {
		return nil, err
	}

	input, err := ort.NewTensor(
		ort.NewShape(1, 3, ModelInputSize, ModelInputSize),
		make([]float32, 3*ModelInputSize*ModelInputSize),
	)
	if err != nil {
		return nil, fmt.Errorf("captcha: create input tensor: %w", err)
	}
	output, err := ort.NewTensor(
		ort.NewShape(1, modelAttributes, modelAnchors),
		make([]float32, modelAttributes*modelAnchors),
	)
	if err != nil {
		input.Destroy()
		return nil, fmt.Errorf("captcha: create output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(
		modelPath,
		[]string{modelInputName},
		[]string{modelOutputName},
		[]ort.Value{input},
		[]ort.Value{output},
		nil,
	)
	if err != nil {
		input.Destroy()
		output.Destroy()
		return nil, fmt.Errorf("captcha: open model %s: %w", modelPath, err)
	}
	return &onnxDetector{session: session, input: input, output: output}, nil
}

func (d *onnxDetector) Run(input []float32) ([]float32, error) {
	if len(input) != len(d.input.GetData()) {
		return nil, fmt.Errorf("captcha: invalid input length %d", len(input))
	}
	copy(d.input.GetData(), input)
	if err := d.session.Run(); err != nil {
		return nil, err
	}
	output := d.output.GetData()
	result := make([]float32, len(output))
	copy(result, output)
	return result, nil
}

func (d *onnxDetector) Close() error {
	var errs []error
	if d.session != nil {
		if err := d.session.Destroy(); err != nil {
			errs = append(errs, err)
		}
		d.session = nil
	}
	if d.input != nil {
		if err := d.input.Destroy(); err != nil {
			errs = append(errs, err)
		}
		d.input = nil
	}
	if d.output != nil {
		if err := d.output.Destroy(); err != nil {
			errs = append(errs, err)
		}
		d.output = nil
	}
	return errors.Join(errs...)
}
