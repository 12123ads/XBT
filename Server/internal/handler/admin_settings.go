package handler

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/common"
	"xbt2/server/internal/config"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/service"
)

const (
	maxAdminQMXLocationPresetCount    = 200
	maxAdminCourseLocationPresetCount = 200
)

type AdminSettingsHandler struct {
	settings *service.RuntimeSettingsService
}

func NewAdminSettingsHandler(settings *service.RuntimeSettingsService) *AdminSettingsHandler {
	return &AdminSettingsHandler{settings: settings}
}

func (h *AdminSettingsHandler) Get(c *gin.Context) {
	settings, err := h.settings.Settings()
	if err != nil {
		common.Fail(c, 500, "query settings failed")
		return
	}
	common.Success(c, settings)
}

func (h *AdminSettingsHandler) Update(c *gin.Context) {
	var req dto.AdminRuntimeSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	courseWebhook, err := normalizeAdminWebhookURL(req.CourseSignWebhookURL, "course_sign_webhook_url")
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	qmxWebhook, err := normalizeAdminWebhookURL(req.QMXAutoSignWebhookURL, "qmx_auto_sign_webhook_url")
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	presets, err := normalizeAdminQMXLocationPresets(req.QMXLocationPresets)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}
	coursePresets, err := normalizeAdminCourseLocationPresets(req.CourseLocationPresets)
	if err != nil {
		common.Fail(c, 400, err.Error())
		return
	}

	settings, err := h.settings.Update(service.RuntimeSettings{
		CourseSignWebhookURL:  courseWebhook,
		QMXAutoSignWebhookURL: qmxWebhook,
		QMXLocationPresets:    presets,
		CourseLocationPresets: coursePresets,
	})
	if err != nil {
		common.Fail(c, 500, "save settings failed")
		return
	}
	common.Success(c, settings)
}

func normalizeAdminWebhookURL(raw, field string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid %s", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid %s", field)
	}
	return raw, nil
}

func normalizeAdminQMXLocationPresets(reqs []dto.AdminQMXLocationPresetRequest) ([]config.QMXLocationPreset, error) {
	if len(reqs) > maxAdminQMXLocationPresetCount {
		return nil, fmt.Errorf("too many QMX location presets")
	}

	presets := make([]config.QMXLocationPreset, 0, len(reqs))
	for i, req := range reqs {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			return nil, fmt.Errorf("location preset %d name is required", i+1)
		}
		if invalidCoordinate(req.Lng) || req.Lng < -180 || req.Lng > 180 {
			return nil, fmt.Errorf("location preset %d lng is invalid", i+1)
		}
		if invalidCoordinate(req.Lat) || req.Lat < -90 || req.Lat > 90 {
			return nil, fmt.Errorf("location preset %d lat is invalid", i+1)
		}
		if req.Range <= 0 || req.Range > 5000 {
			return nil, fmt.Errorf("location preset %d range is invalid", i+1)
		}
		presets = append(presets, config.QMXLocationPreset{
			Name:  name,
			Lng:   req.Lng,
			Lat:   req.Lat,
			Range: req.Range,
		})
	}
	return presets, nil
}

func normalizeAdminCourseLocationPresets(reqs []dto.AdminCourseLocationPresetRequest) ([]config.CourseLocationPreset, error) {
	if len(reqs) > maxAdminCourseLocationPresetCount {
		return nil, fmt.Errorf("too many course location presets")
	}

	presets := make([]config.CourseLocationPreset, 0, len(reqs))
	for i, req := range reqs {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			return nil, fmt.Errorf("course location preset %d name is required", i+1)
		}
		lng, err := normalizeCoordinateText(req.Lng, -180, 180)
		if err != nil {
			return nil, fmt.Errorf("course location preset %d lng is invalid", i+1)
		}
		lat, err := normalizeCoordinateText(req.Lat, -90, 90)
		if err != nil {
			return nil, fmt.Errorf("course location preset %d lat is invalid", i+1)
		}
		description := strings.TrimSpace(req.Description)
		if description == "" {
			return nil, fmt.Errorf("course location preset %d description is required", i+1)
		}
		presets = append(presets, config.CourseLocationPreset{
			Name:        name,
			Lng:         lng,
			Lat:         lat,
			Description: description,
		})
	}
	return presets, nil
}

func normalizeCoordinateText(raw string, minValue, maxValue float64) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("coordinate is required")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || invalidCoordinate(value) || value < minValue || value > maxValue {
		return "", fmt.Errorf("coordinate is invalid")
	}
	return raw, nil
}

func invalidCoordinate(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0)
}
