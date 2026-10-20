package xxt

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	captchaVersion       = "1.1.20"
	captchaTokenTTL      = 300000 // 毫秒；上游 token 的有效期
	captchaMaxChallenges = 3      // 单次签到最多重取几次验证码，避免放大风控
)

// captchaChallenge 是一次性滑动验证码状态。
// 上游对同一个 token 只接受一次 x 提交，失败必须重新取图，不能换 x 重试。
type captchaChallenge struct {
	token       string
	iv          string
	shadeImage  string
	cutoutImage string
}

func (c *Client) fetchCaptchaValidate(cli *http.Client, fixed FixedParams) (string, error) {
	referer := fmt.Sprintf(
		"https://mobilelearn.chaoxing.com/page/sign/signIn?courseId=%d&classId=%d&activeId=%d&fid=0&timetable=0",
		fixed.CourseID, fixed.ClassID, fixed.ActiveID,
	)

	var lastErr error
	for attempt := 1; attempt <= captchaMaxChallenges; attempt++ {
		validate, err := c.solveCaptchaChallenge(cli, referer, fixed, attempt)
		if err == nil {
			return validate, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// solveCaptchaChallenge 取一张新验证码、只提交一次 x，并把结果写入埋点日志。
func (c *Client) solveCaptchaChallenge(cli *http.Client, referer string, fixed FixedParams, attempt int) (string, error) {
	challenge, err := c.newCaptchaChallenge(cli, referer)
	if err != nil {
		logCaptchaAttempt(fixed, attempt, "-", 0, 0, "-", "challenge_error")
		return "", err
	}

	bgBytes, err := c.downloadBytes(cli, challenge.shadeImage, referer)
	if err != nil {
		logCaptchaAttempt(fixed, attempt, "-", 0, 0, "-", "shade_download_error")
		return "", err
	}
	challengeID := captchaChallengeID(bgBytes)

	targetBytes, err := c.downloadBytes(cli, challenge.cutoutImage, referer)
	if err != nil {
		logCaptchaAttempt(fixed, attempt, challengeID, 0, 0, "-", "cutout_download_error")
		return "", err
	}

	candidates, source, err := c.detectSlideXCandidates(targetBytes, bgBytes)
	if err != nil {
		logCaptchaAttempt(fixed, attempt, challengeID, 0, 0, source, "detect_error")
		return "", err
	}

	// 只提交本地评分最高的一个候选；失败就换 challenge，不再拿同一个 token 试探。
	top1 := candidates[0]
	validate, err := c.verifyCaptchaAtX(cli, referer, challenge, top1)
	result := "ok"
	if err != nil {
		result = "rejected"
	}
	logCaptchaAttempt(fixed, attempt, challengeID, len(candidates), top1, source, result)
	return validate, err
}

// newCaptchaChallenge 按上游客户端流程取一张验证码：
// get/conf 拿服务端时间 t → 由 t 派生 captchaKey/token → 取 shadeImage/cutoutImage。
func (c *Client) newCaptchaChallenge(cli *http.Client, referer string) (captchaChallenge, error) {
	t, err := c.fetchCaptchaConf(cli)
	if err != nil {
		return captchaChallenge{}, err
	}

	tStr := strconv.FormatInt(t, 10)
	nonce := randomString(32)
	captchaKey := md5Hex(tStr + nonce)
	iv := md5Hex(captchaID + captchaType + strconv.FormatInt(time.Now().UnixMilli(), 10) + nonce)
	token := md5Hex(tStr+captchaID+captchaType+captchaKey) + ":" + strconv.FormatInt(t+captchaTokenTTL, 10)

	imageURL := "https://captcha.chaoxing.com/captcha/get/verification/image"
	q := url.Values{}
	q.Set("callback", "cx_captcha_function")
	q.Set("captchaId", captchaID)
	q.Set("type", captchaType)
	q.Set("version", captchaVersion)
	q.Set("captchaKey", captchaKey)
	q.Set("token", token)
	q.Set("iv", iv)
	q.Set("referer", referer)
	q.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))

	req, _ := http.NewRequest(http.MethodGet, imageURL+"?"+q.Encode(), nil)
	setCaptchaHeaders(req, referer)
	resp, err := cli.Do(req)
	if err != nil {
		return captchaChallenge{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	obj, err := extractJSONPObject(body)
	if err != nil {
		return captchaChallenge{}, err
	}

	var payload struct {
		Token               string `json:"token"`
		ImageVerificationVo struct {
			ShadeImage  string `json:"shadeImage"`
			CutoutImage string `json:"cutoutImage"`
		} `json:"imageVerificationVo"`
	}
	if err := json.Unmarshal(obj, &payload); err != nil {
		return captchaChallenge{}, err
	}
	if payload.Token == "" || payload.ImageVerificationVo.ShadeImage == "" || payload.ImageVerificationVo.CutoutImage == "" {
		return captchaChallenge{}, errors.New("captcha payload incomplete")
	}

	return captchaChallenge{
		token:       payload.Token,
		iv:          iv,
		shadeImage:  payload.ImageVerificationVo.ShadeImage,
		cutoutImage: payload.ImageVerificationVo.CutoutImage,
	}, nil
}

// fetchCaptchaConf 取服务端时间 t，token 的摘要和时间戳后缀都必须基于它。
func (c *Client) fetchCaptchaConf(cli *http.Client) (int64, error) {
	confURL := "https://captcha.chaoxing.com/captcha/get/conf"
	q := url.Values{}
	q.Set("captchaId", captchaID)
	q.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))

	req, _ := http.NewRequest(http.MethodGet, confURL+"?"+q.Encode(), nil)
	setCaptchaHeaders(req, "")
	resp, err := cli.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	obj, err := extractJSONPObject(body)
	if err != nil {
		return 0, err
	}

	var conf struct {
		T int64 `json:"t"`
	}
	if err := json.Unmarshal(obj, &conf); err != nil {
		return 0, err
	}
	if conf.T <= 0 {
		return 0, errors.New("captcha conf missing t")
	}
	return conf.T, nil
}

func (c *Client) verifyCaptchaAtX(cli *http.Client, referer string, challenge captchaChallenge, x int) (string, error) {
	checkURL := "https://captcha.chaoxing.com/captcha/check/verification/result"
	q2 := url.Values{}
	q2.Set("callback", "cx_captcha_function")
	q2.Set("captchaId", captchaID)
	q2.Set("type", captchaType)
	q2.Set("token", challenge.token)
	q2.Set("textClickArr", fmt.Sprintf("[{\"x\":%d}]", x))
	q2.Set("coordinate", "[]")
	q2.Set("runEnv", "10")
	q2.Set("version", captchaVersion)
	q2.Set("iv", challenge.iv)
	q2.Set("t", "a")
	q2.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))

	req2, _ := http.NewRequest(http.MethodGet, checkURL+"?"+q2.Encode(), nil)
	setCaptchaHeaders(req2, referer)
	resp2, err := cli.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	obj2, err := extractJSONPObject(body2)
	if err != nil {
		return "", err
	}

	var verify struct {
		Result    bool   `json:"result"`
		Error     int    `json:"error"`
		Msg       string `json:"msg"`
		ExtraData string `json:"extraData"`
	}
	if err := json.Unmarshal(obj2, &verify); err != nil {
		return "", err
	}
	if verify.Error == 1 {
		if verify.Msg == "" {
			verify.Msg = "captcha check error"
		}
		return "", errors.New(verify.Msg)
	}
	if !verify.Result {
		return "", errors.New("captcha verify failed")
	}

	var extra struct {
		Validate string `json:"validate"`
	}
	if err := json.Unmarshal([]byte(verify.ExtraData), &extra); err != nil {
		return "", err
	}
	if extra.Validate == "" {
		return "", errors.New("validate empty")
	}
	return extra.Validate, nil
}

// captchaChallengeID 用背景图摘要做埋点标识，避免把 token 或原图写进日志。
func captchaChallengeID(shade []byte) string {
	sum := md5.Sum(shade)
	return hex.EncodeToString(sum[:6])
}

func logCaptchaAttempt(fixed FixedParams, attempt int, challengeID string, candidates, top1 int, source, result string) {
	log.Printf(
		"captcha attempt activity=%d uid=%d attempt=%d challenge_id=%s captcha_model=%s candidate_count=%d top1_x=%d upstream_result=%s",
		fixed.ActiveID, fixed.UID, attempt, challengeID, source, candidates, top1, result,
	)
}

func (c *Client) downloadBytes(cli *http.Client, rawURL string, referer string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	setCaptchaHeaders(req, referer)
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func setCaptchaHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	req.Header.Set("Accept", "*/*")
}

func extractJSONPObject(body []byte) ([]byte, error) {
	s := string(body)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < 0 || end <= start {
		return nil, errors.New("invalid jsonp")
	}
	return []byte(s[start : end+1]), nil
}

func randomString(n int) string {
	const chars = "ABCDEFGHJKMNPQRSTWXYZabcdefhijkmnprstwxyz2345678"
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	for i := 0; i < n; i++ {
		buf[i] = chars[int(raw[i])%len(chars)]
	}
	return string(buf)
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// slideXPredictor is satisfied by *captcha.Predictor. The interface keeps xxt
// testable without ONNX Runtime.
type slideXPredictor interface {
	PredictSlideX(img []byte) (int, error)
}

// SetCaptchaPredictor wires the ONNX slide-gap detector. A nil predictor (or a
// failed prediction) keeps the heuristic path. Call before serving traffic.
func (c *Client) SetCaptchaPredictor(predictor slideXPredictor) {
	c.captchaModel = predictor
}

// detectSlideXCandidates returns candidate x offsets and the source used for
// the first candidate. A working model supplies the only candidate; otherwise
// the legacy heuristic runs.
func (c *Client) detectSlideXCandidates(targetBytes, bgBytes []byte) ([]int, string, error) {
	if c.captchaModel != nil {
		x, err := c.captchaModel.PredictSlideX(bgBytes)
		if err == nil {
			return []int{x}, "model", nil
		}
		log.Printf("captcha_model=fallback reason=%v", err)
	} else {
		log.Printf("captcha_model=fallback reason=not_configured")
	}
	candidates, err := detectSlideXCandidatesHeuristic(targetBytes, bgBytes)
	return candidates, "fallback", err
}

func detectSlideXCandidatesHeuristic(targetBytes, bgBytes []byte) ([]int, error) {
	targetImg, _, err := image.Decode(bytes.NewReader(targetBytes))
	if err != nil {
		return nil, err
	}
	bgImg, _, err := image.Decode(bytes.NewReader(bgBytes))
	if err != nil {
		return nil, err
	}
	tb := targetImg.Bounds()
	bb := bgImg.Bounds()
	tw, th := tb.Dx(), tb.Dy()
	bw, bh := bb.Dx(), bb.Dy()
	if tw <= 0 || th <= 0 || bw <= 0 || bh <= 0 {
		return nil, errors.New("invalid image size")
	}

	limitY := minInt(th, bh)
	type pt struct{ x, y int }
	mask := make([]pt, 0, tw*limitY/2)
	for y := 0; y < limitY; y += 2 {
		for x := 0; x < tw; x += 2 {
			_, _, _, a := targetImg.At(tb.Min.X+x, tb.Min.Y+y).RGBA()
			if a>>8 < 32 {
				continue
			}
			mask = append(mask, pt{x: x, y: y})
		}
	}
	if len(mask) < 80 {
		return nil, errors.New("captcha mask too small")
	}

	maxX := bw - tw
	if maxX <= 1 {
		return nil, errors.New("captcha width mismatch")
	}

	type match struct {
		x     int
		score float64
	}
	matches := make([]match, 0, maxX+1)
	edge := make([]float64, maxX+1)
	for offX := 0; offX <= maxX; offX++ {
		score := 0.0
		for _, p := range mask {
			tg := gray(targetImg.At(tb.Min.X+p.x, tb.Min.Y+p.y))
			bg := gray(bgImg.At(bb.Min.X+offX+p.x, bb.Min.Y+p.y))
			score += math.Abs(tg - bg)
		}
		score /= float64(len(mask))
		matches = append(matches, match{x: offX, score: score})

		es := 0.0
		for y := 0; y < limitY; y += 2 {
			l := gray(bgImg.At(bb.Min.X+offX, bb.Min.Y+y))
			r := gray(bgImg.At(bb.Min.X+offX+tw-1, bb.Min.Y+y))
			es += math.Abs(r - l)
		}
		edge[offX] = es
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].score < matches[j].score })

	tgtColEdge := make([]float64, tw)
	for x := 1; x < tw-1; x++ {
		s := 0.0
		for y := 0; y < limitY; y += 2 {
			l := gray(targetImg.At(tb.Min.X+x-1, tb.Min.Y+y))
			r := gray(targetImg.At(tb.Min.X+x+1, tb.Min.Y+y))
			s += math.Abs(r - l)
		}
		tgtColEdge[x] = s
	}
	bgColEdge := make([]float64, bw)
	for x := 1; x < bw-1; x++ {
		s := 0.0
		for y := 0; y < limitY; y += 2 {
			l := gray(bgImg.At(bb.Min.X+x-1, bb.Min.Y+y))
			r := gray(bgImg.At(bb.Min.X+x+1, bb.Min.Y+y))
			s += math.Abs(r - l)
		}
		bgColEdge[x] = s
	}
	profileMatches := make([]match, 0, maxX+1)
	for offX := 0; offX <= maxX; offX++ {
		s := 0.0
		n := 0
		for x := 1; x < tw-1; x++ {
			bx := offX + x
			if bx <= 0 || bx >= bw-1 {
				continue
			}
			s += math.Abs(tgtColEdge[x] - bgColEdge[bx])
			n++
		}
		if n == 0 {
			continue
		}
		profileMatches = append(profileMatches, match{x: offX, score: s / float64(n)})
	}
	sort.Slice(profileMatches, func(i, j int) bool { return profileMatches[i].score < profileMatches[j].score })

	edgeIdx := make([]int, 0, len(edge))
	for i := range edge {
		edgeIdx = append(edgeIdx, i)
	}
	sort.Slice(edgeIdx, func(i, j int) bool { return edge[edgeIdx[i]] > edge[edgeIdx[j]] })

	limit := minInt(12, len(matches))
	edgeLimit := minInt(8, len(edgeIdx))
	cands := make([]int, 0, 64)
	seen := make(map[int]struct{}, 64)
	add := func(v int) {
		if v < 0 || v > maxX {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		cands = append(cands, v)
	}
	for i := 0; i < limit; i++ {
		base := matches[i].x
		for _, d := range []int{0, -1, 1, -2, 2, -3, 3, -5, 5, -8, 8} {
			add(base + d)
		}
	}
	profileLimit := minInt(8, len(profileMatches))
	for i := 0; i < profileLimit; i++ {
		base := profileMatches[i].x
		for _, d := range []int{0, -1, 1, -2, 2, -4, 4, -6, 6} {
			add(base + d)
		}
	}
	for i := 0; i < edgeLimit; i++ {
		base := edgeIdx[i]
		for _, d := range []int{0, -2, 2, -4, 4} {
			add(base + d)
		}
	}
	if len(cands) == 0 {
		return nil, errors.New("failed to locate slider x")
	}
	if len(cands) > 40 {
		cands = cands[:40]
	}
	return cands, nil
}

func gray(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	rf := float64(r >> 8)
	gf := float64(g >> 8)
	bf := float64(b >> 8)
	return 0.299*rf + 0.587*gf + 0.114*bf
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
