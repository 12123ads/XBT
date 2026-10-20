//go:build !cgo

package captcha

func newDetector(string) (detector, error) {
	return nil, ErrRuntimeUnavailable
}
