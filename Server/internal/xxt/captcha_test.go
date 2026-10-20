package xxt

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	captchaTestMobile   = "13800000000"
	captchaTestPassword = "secret"
	captchaTestConfT    = int64(1700000000000)
)

var captchaTestTokenPattern = regexp.MustCompile(`^[0-9a-f]{32}:\d+$`)

// captchaTestImages 造一对可解的验证码图片：背景 320x160，滑块条 56x160，
// 不透明方块对应背景 x=100 处的同一块像素，因此正确答案固定是 100。
func captchaTestImages(t *testing.T) (shade, cutout []byte, gapX int) {
	t.Helper()
	const (
		bgWidth  = 320
		bgHeight = 160
		pieceW   = 56
		answerX  = 100
	)

	bg := image.NewRGBA(image.Rect(0, 0, bgWidth, bgHeight))
	for y := 0; y < bgHeight; y++ {
		for x := 0; x < bgWidth; x++ {
			bg.SetRGBA(x, y, color.RGBA{
				R: uint8((x*17 + y*29) % 251),
				G: uint8((x*31 + y*11) % 253),
				B: uint8((x*7 + y*53) % 247),
				A: 255,
			})
		}
	}

	piece := image.NewNRGBA(image.Rect(0, 0, pieceW, bgHeight))
	for y := 40; y < 120; y++ {
		for x := 8; x < 48; x++ {
			c := bg.RGBAAt(answerX+x, y)
			piece.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255})
		}
	}

	var shadeBuf, cutoutBuf bytes.Buffer
	if err := png.Encode(&shadeBuf, bg); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&cutoutBuf, piece); err != nil {
		t.Fatal(err)
	}
	return shadeBuf.Bytes(), cutoutBuf.Bytes(), answerX
}

func newCaptchaTestClient(t *testing.T, transport learningTestTransport) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(&url.URL{Scheme: "https", Host: "captcha.chaoxing.com", Path: "/"}, []*http.Cookie{
		{Name: "_uid", Value: "12345", Domain: ".chaoxing.com", Path: "/", Secure: true},
	})
	return &Client{
		mobileUA: "captcha-test",
		http:     &http.Client{Transport: transport, Timeout: 5 * time.Second},
		sessions: map[string]*Session{
			captchaTestMobile: {
				Mobile:      captchaTestMobile,
				Password:    captchaTestPassword,
				Jar:         jar,
				LastLoginAt: time.Now(),
			},
		},
	}
}

func captchaTestPNG(req *http.Request, data []byte) *http.Response {
	resp := learningTestResponse(req, http.StatusOK, "")
	resp.Header.Set("Content-Type", "image/png")
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp
}

// captchaTestHTTP 复现 Sign 的做法：在基础 client 上挂会话 cookie jar。
func captchaTestHTTP(client *Client) *http.Client {
	cli := *client.http
	cli.Jar = client.sessions[captchaTestMobile].Jar
	return &cli
}

func captchaTestSign(fixed FixedParams) FixedParams {
	fixed.ActiveID = 4242
	fixed.UID = 777
	if fixed.CourseID == 0 {
		fixed.CourseID = 9
	}
	if fixed.ClassID == 0 {
		fixed.ClassID = 8
	}
	return fixed
}

func TestFetchCaptchaValidateReusesSessionAndRetriesWithNewChallenge(t *testing.T) {
	shade, cutout, answerX := captchaTestImages(t)

	var confRequests int
	var imageRequests []url.Values
	var checkRequests []url.Values

	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		if cookie, err := req.Cookie("_uid"); err != nil || cookie.Value != "12345" {
			return nil, fmt.Errorf("captcha request %s lost the session cookie: %v", req.URL.Path, err)
		}
		switch req.URL.Path {
		case "/captcha/get/conf":
			confRequests++
			return learningTestResponse(req, http.StatusOK,
				fmt.Sprintf(`cx_captcha_function({"t":%d})`, captchaTestConfT)), nil
		case "/captcha/get/verification/image":
			imageRequests = append(imageRequests, req.URL.Query())
			token := fmt.Sprintf("token-%d", len(imageRequests))
			body := fmt.Sprintf(
				`cx_captcha_function({"token":%q,"imageVerificationVo":{"shadeImage":"https://captcha.chaoxing.com/shade.png","cutoutImage":"https://captcha.chaoxing.com/cutout.png"}})`,
				token,
			)
			return learningTestResponse(req, http.StatusOK, body), nil
		case "/shade.png":
			return captchaTestPNG(req, shade), nil
		case "/cutout.png":
			return captchaTestPNG(req, cutout), nil
		case "/captcha/check/verification/result":
			checkRequests = append(checkRequests, req.URL.Query())
			if len(checkRequests) == 1 {
				return learningTestResponse(req, http.StatusOK, `cx_captcha_function({"result":false})`), nil
			}
			return learningTestResponse(req, http.StatusOK,
				`cx_captcha_function({"result":true,"extraData":"{\"validate\":\"validate-ok\"}"})`), nil
		}
		return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
	})

	client := newCaptchaTestClient(t, transport)
	validate, err := client.fetchCaptchaValidate(captchaTestHTTP(client), captchaTestSign(FixedParams{}))
	if err != nil {
		t.Fatalf("fetchCaptchaValidate error = %v", err)
	}
	if validate != "validate-ok" {
		t.Fatalf("validate = %q, want validate-ok", validate)
	}

	if confRequests != 2 {
		t.Fatalf("get/conf requests = %d, want 2 (a rejected challenge must be replaced)", confRequests)
	}
	if len(imageRequests) != 2 {
		t.Fatalf("image requests = %d, want 2", len(imageRequests))
	}
	expiry := strconv.FormatInt(captchaTestConfT+captchaTokenTTL, 10)
	for i, q := range imageRequests {
		for _, key := range []string{"captchaKey", "iv", "token"} {
			if q.Get(key) == "" {
				t.Fatalf("image request %d missing %s", i+1, key)
			}
		}
		token := q.Get("token")
		if !captchaTestTokenPattern.MatchString(token) {
			t.Fatalf("image request %d token = %q, want md5:expiry", i+1, token)
		}
		if suffix := token[len(token)-len(expiry):]; suffix != expiry {
			t.Fatalf("image request %d token expiry = %q, want %q", i+1, suffix, expiry)
		}
		if len(q.Get("captchaKey")) != 32 || len(q.Get("iv")) != 32 {
			t.Fatalf("image request %d captchaKey/iv must be md5 hex: %v", i+1, q)
		}
	}

	if len(checkRequests) != 2 {
		t.Fatalf("check requests = %d, want 2 (one x per challenge token)", len(checkRequests))
	}
	seen := map[string]struct{}{}
	for i, q := range checkRequests {
		token := q.Get("token")
		if _, ok := seen[token]; ok {
			t.Fatalf("token %q submitted more than once", token)
		}
		seen[token] = struct{}{}
		if token != fmt.Sprintf("token-%d", i+1) {
			t.Fatalf("check %d used token %q, want the token returned with that challenge", i+1, token)
		}
		if q.Get("t") != "a" {
			t.Fatalf("check %d missing t=a", i+1)
		}
		if q.Get("iv") != imageRequests[i].Get("iv") {
			t.Fatalf("check %d iv = %q, want challenge iv %q", i+1, q.Get("iv"), imageRequests[i].Get("iv"))
		}
		if q.Get("textClickArr") != fmt.Sprintf(`[{"x":%d}]`, answerX) {
			t.Fatalf("check %d textClickArr = %q, want x=%d", i+1, q.Get("textClickArr"), answerX)
		}
	}
}

func TestFetchCaptchaValidateGivesUpAfterChallengeLimit(t *testing.T) {
	shade, cutout, _ := captchaTestImages(t)

	var confRequests, checkRequests int
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/captcha/get/conf":
			confRequests++
			return learningTestResponse(req, http.StatusOK,
				fmt.Sprintf(`cx_captcha_function({"t":%d})`, captchaTestConfT)), nil
		case "/captcha/get/verification/image":
			body := fmt.Sprintf(
				`cx_captcha_function({"token":"token-%d","imageVerificationVo":{"shadeImage":"https://captcha.chaoxing.com/shade.png","cutoutImage":"https://captcha.chaoxing.com/cutout.png"}})`,
				confRequests,
			)
			return learningTestResponse(req, http.StatusOK, body), nil
		case "/shade.png":
			return captchaTestPNG(req, shade), nil
		case "/cutout.png":
			return captchaTestPNG(req, cutout), nil
		case "/captcha/check/verification/result":
			checkRequests++
			return learningTestResponse(req, http.StatusOK, `cx_captcha_function({"result":false})`), nil
		}
		return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
	})

	client := newCaptchaTestClient(t, transport)
	if _, err := client.fetchCaptchaValidate(captchaTestHTTP(client), captchaTestSign(FixedParams{})); err == nil {
		t.Fatal("expected an error when every challenge is rejected")
	}
	if confRequests != captchaMaxChallenges || checkRequests != captchaMaxChallenges {
		t.Fatalf("conf requests = %d, checks = %d, want %d each", confRequests, checkRequests, captchaMaxChallenges)
	}
}

func TestFetchCaptchaConfParsesTopLevelAndNestedT(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"top-level", fmt.Sprintf(`cx_captcha_function({"t":%d,"captchaId":%q})`, captchaTestConfT, captchaID)},
		{"nested data", fmt.Sprintf(`cx_captcha_function({"captchaId":%q,"data":{"t":%d}})`, captchaID, captchaTestConfT)},
		{"nested result", fmt.Sprintf(`cx_captcha_function({"result":{"t":%d}})`, captchaTestConfT)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotCallback, gotReferer string
			transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/captcha/get/conf" {
					return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
				}
				gotCallback = req.URL.Query().Get("callback")
				gotReferer = req.Header.Get("Referer")
				return learningTestResponse(req, http.StatusOK, tc.body), nil
			})

			client := newCaptchaTestClient(t, transport)
			referer := "https://mobilelearn.chaoxing.com/page/sign/signIn"
			got, err := client.fetchCaptchaConf(captchaTestHTTP(client), referer)
			if err != nil {
				t.Fatalf("fetchCaptchaConf error = %v", err)
			}
			if got != captchaTestConfT {
				t.Fatalf("t = %d, want %d", got, captchaTestConfT)
			}
			if gotCallback != captchaCallback {
				t.Fatalf("callback = %q, want %q (upstream returns CALLBACK ERROR without it)", gotCallback, captchaCallback)
			}
			if gotReferer != referer {
				t.Fatalf("Referer = %q, want %q", gotReferer, referer)
			}
		})
	}
}

func TestFetchCaptchaConfMissingTReturnsCodedErrorWithSnippet(t *testing.T) {
	body := `cx_captcha_function({"captchaId":"42sx","msg":"upstream changed shape"})`
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		return learningTestResponse(req, http.StatusOK, body), nil
	})

	client := newCaptchaTestClient(t, transport)
	_, err := client.fetchCaptchaConf(captchaTestHTTP(client), "https://mobilelearn.chaoxing.com/")
	if err == nil {
		t.Fatal("expected an error when t is absent")
	}
	var confErr *captchaConfError
	if !errors.As(err, &confErr) {
		t.Fatalf("error type = %T, want *captchaConfError", err)
	}
	if confErr.Code != captchaConfCodeMissingT {
		t.Fatalf("code = %q, want %q", confErr.Code, captchaConfCodeMissingT)
	}
	if !strings.Contains(confErr.Error(), captchaConfCodeMissingT) {
		t.Fatalf("error = %q, want the explicit code %q", confErr.Error(), captchaConfCodeMissingT)
	}
	if !strings.Contains(confErr.Error(), "upstream changed shape") {
		t.Fatalf("error = %q, want a response snippet", confErr.Error())
	}
}

func TestFetchCaptchaConfCallbackErrorIsCoded(t *testing.T) {
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		return learningTestResponse(req, http.StatusOK, `{"10013":"CALLBACK ERROR"}`), nil
	})

	client := newCaptchaTestClient(t, transport)
	_, err := client.fetchCaptchaConf(captchaTestHTTP(client), "https://mobilelearn.chaoxing.com/")
	var confErr *captchaConfError
	if !errors.As(err, &confErr) {
		t.Fatalf("error type = %T, want *captchaConfError", err)
	}
	if confErr.Code != captchaConfCodeCallbackError {
		t.Fatalf("code = %q, want %q", confErr.Code, captchaConfCodeCallbackError)
	}
	if !strings.Contains(confErr.Error(), "CALLBACK ERROR") {
		t.Fatalf("error = %q, want the upstream snippet", confErr.Error())
	}
}

func TestCaptchaConfSnippetTruncatesAndRedacts(t *testing.T) {
	body := `{"token":"super-secret","validate":"validate-secret","padding":"` + strings.Repeat("x", 400) + `"}`
	snippet := captchaConfSnippet([]byte(body))
	if strings.Contains(snippet, "super-secret") || strings.Contains(snippet, "validate-secret") {
		t.Fatalf("snippet leaked a secret: %q", snippet)
	}
	if !strings.Contains(snippet, `"token":"***"`) || !strings.Contains(snippet, `"validate":"***"`) {
		t.Fatalf("snippet = %q, want redacted values", snippet)
	}
	if len(snippet) > captchaConfSnippetLimit+len("...") {
		t.Fatalf("snippet length = %d, want <= %d", len(snippet), captchaConfSnippetLimit+len("..."))
	}
}

func TestCaptchaConfFailureLogIncludesSnippet(t *testing.T) {
	body := `cx_captcha_function({"captchaId":"42sx","msg":"upstream changed shape"})`
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/captcha/get/conf" {
			return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
		}
		return learningTestResponse(req, http.StatusOK, body), nil
	})

	client := newCaptchaTestClient(t, transport)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	if _, err := client.fetchCaptchaValidate(captchaTestHTTP(client), captchaTestSign(FixedParams{})); err == nil {
		t.Fatal("expected captcha failure")
	}
	for _, want := range []string{captchaConfCodeMissingT, "upstream changed shape", "captcha challenge failed"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("logs = %q, want %q", logs.String(), want)
		}
	}
}

func TestNewCaptchaChallengeRejectsConfWithoutT(t *testing.T) {
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/captcha/get/conf" {
			return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
		}
		return learningTestResponse(req, http.StatusOK, `cx_captcha_function({"t":0})`), nil
	})

	client := newCaptchaTestClient(t, transport)
	if _, err := client.newCaptchaChallenge(captchaTestHTTP(client), "https://mobilelearn.chaoxing.com/"); err == nil {
		t.Fatal("expected an error for a missing server timestamp")
	}
}

func TestCaptchaCheckErrorIsSurfaced(t *testing.T) {
	shade, cutout, _ := captchaTestImages(t)
	transport := learningTestTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/captcha/get/conf":
			return learningTestResponse(req, http.StatusOK,
				fmt.Sprintf(`cx_captcha_function({"t":%d})`, captchaTestConfT)), nil
		case "/captcha/get/verification/image":
			return learningTestResponse(req, http.StatusOK,
				`cx_captcha_function({"token":"token-1","imageVerificationVo":{"shadeImage":"https://captcha.chaoxing.com/shade.png","cutoutImage":"https://captcha.chaoxing.com/cutout.png"}})`), nil
		case "/shade.png":
			return captchaTestPNG(req, shade), nil
		case "/cutout.png":
			return captchaTestPNG(req, cutout), nil
		case "/captcha/check/verification/result":
			return learningTestResponse(req, http.StatusOK,
				`cx_captcha_function({"error":1,"msg":"验证码已失效"})`), nil
		}
		return nil, fmt.Errorf("unexpected captcha request: %s", req.URL)
	})

	client := newCaptchaTestClient(t, transport)
	_, err := client.fetchCaptchaValidate(captchaTestHTTP(client), captchaTestSign(FixedParams{}))
	if err == nil || err.Error() != "验证码已失效" {
		t.Fatalf("error = %v, want upstream message", err)
	}
}

type captchaStubPredictor struct {
	x   int
	err error
}

func (s captchaStubPredictor) PredictSlideX([]byte) (int, error) {
	return s.x, s.err
}

func TestDetectSlideXCandidatesUsesModelAsOnlyCandidate(t *testing.T) {
	shade, cutout, _ := captchaTestImages(t)
	client := newCaptchaTestClient(t, nil)
	client.SetCaptchaPredictor(captchaStubPredictor{x: 77})

	candidates, source, err := client.detectSlideXCandidates(cutout, shade)
	if err != nil {
		t.Fatalf("detectSlideXCandidates error = %v", err)
	}
	if source != "model" {
		t.Fatalf("source = %q, want model", source)
	}
	if len(candidates) != 1 || candidates[0] != 77 {
		t.Fatalf("candidates = %v, want [77]", candidates)
	}
}

func TestDetectSlideXCandidatesFallsBackToHeuristic(t *testing.T) {
	shade, cutout, _ := captchaTestImages(t)
	client := newCaptchaTestClient(t, nil)
	client.SetCaptchaPredictor(captchaStubPredictor{err: fmt.Errorf("inference failed")})

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	candidates, source, err := client.detectSlideXCandidates(cutout, shade)
	if err != nil {
		t.Fatalf("detectSlideXCandidates error = %v", err)
	}
	if source != "fallback" {
		t.Fatalf("source = %q, want fallback", source)
	}
	if len(candidates) == 0 {
		t.Fatal("candidates empty, want heuristic candidates")
	}
	if !strings.Contains(logs.String(), "captcha_model=fallback") {
		t.Fatalf("logs = %q, want captcha_model=fallback", logs.String())
	}
	if !strings.Contains(logs.String(), "inference failed") {
		t.Fatalf("logs = %q, want inference error", logs.String())
	}
}
