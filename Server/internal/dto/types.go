package dto

type LoginRequest struct {
	Mobile   string `json:"mobile" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Token string      `json:"token"`
	User  interface{} `json:"user"`
}

type UpdateCourseSelectionRequest struct {
	CourseIDs []int64 `json:"course_ids" binding:"required"`
}

type SignExecuteRequest struct {
	ActivityID    int64                  `json:"activity_id" binding:"required"`
	TargetUID     int64                  `json:"target_uid"`
	UserIDs       []int64                `json:"user_ids"` // backward compatibility, first element is used if target_uid is empty
	SignType      int                    `json:"sign_type"`
	CourseID      int64                  `json:"course_id" binding:"required"`
	ClassID       int64                  `json:"class_id" binding:"required"`
	IfRefreshEWM  bool                   `json:"if_refresh_ewm"`
	ActivityName  string                 `json:"activity_name"`
	CourseName    string                 `json:"course_name"`
	CourseTeacher string                 `json:"course_teacher"`
	Special       map[string]interface{} `json:"special_params"`
}

type SignCheckRequest struct {
	ActivityID int64   `json:"activity_id" binding:"required"`
	UserIDs    []int64 `json:"user_ids"`
}

type SignShareCreateRequest struct {
	ActivityID    int64  `json:"activity_id" binding:"required"`
	CourseID      int64  `json:"course_id" binding:"required"`
	ClassID       int64  `json:"class_id" binding:"required"`
	SignType      int    `json:"sign_type"`
	IfRefreshEWM  bool   `json:"if_refresh_ewm"`
	ActivityName  string `json:"activity_name"`
	CourseName    string `json:"course_name"`
	CourseTeacher string `json:"course_teacher"`
	EndTime       int64  `json:"end_time" binding:"required"`
}

type SignShareExecuteRequest struct {
	Special map[string]interface{} `json:"special_params"`
}

type QMXRoomCheckCredentialRequest struct {
	QMXURL string `json:"qmx_url"`
	XToken string `json:"x_token"`
	Cookie string `json:"cookie"`
	Raw    string `json:"raw"`
}

type QMXRoomCheckPreviewRequest struct {
	QMXRoomCheckCredentialRequest
}

type QMXRoomCheckExecuteRequest struct {
	QMXRoomCheckCredentialRequest
	LocationIndex int     `json:"location_index"`
	Longitude     float64 `json:"longitude"`
	Latitude      float64 `json:"latitude"`
	LocationName  string  `json:"location_name"`
}

type AddWhitelistRequest struct {
	Mobile string `json:"mobile" binding:"required"`
}

type BatchWhitelistRequest struct {
	Mobiles string `json:"mobiles" binding:"required"`
}

type AdminCreateAccountRequest struct {
	Mobile   string `json:"mobile" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type CourseRef struct {
	CourseID int64 `json:"course_id" binding:"required"`
	ClassID  int64 `json:"class_id" binding:"required"`
}

type AdminUpdateCourseSelectionRequest struct {
	Courses []CourseRef `json:"courses" binding:"required"`
}

type AdminAddUserCourseRequest struct {
	CourseID   int64  `json:"course_id" binding:"required"`
	ClassID    int64  `json:"class_id" binding:"required"`
	Name       string `json:"name"`
	Teacher    string `json:"teacher"`
	Icon       string `json:"icon"`
	IsSelected *bool  `json:"is_selected"`
}

type AdminCopyCourseSelectionRequest struct {
	SourceUID  int64   `json:"source_uid" binding:"required"`
	TargetUIDs []int64 `json:"target_uids" binding:"required"`
}

type AdminClassGroupRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type AdminClassGroupMembersRequest struct {
	UserUIDs []int64 `json:"user_uids"`
}

type AdminClassGroupCopySelectionRequest struct {
	SourceUID int64  `json:"source_uid" binding:"required"`
	Mode      string `json:"mode" binding:"required"`
}

type AdminQMXLocationPresetRequest struct {
	Name  string  `json:"name"`
	Lng   float64 `json:"lng"`
	Lat   float64 `json:"lat"`
	Range int     `json:"range"`
}

type AdminRuntimeSettingsRequest struct {
	CourseSignWebhookURL  string                          `json:"course_sign_webhook_url"`
	QMXAutoSignWebhookURL string                          `json:"qmx_auto_sign_webhook_url"`
	QMXLocationPresets    []AdminQMXLocationPresetRequest `json:"qmx_location_presets"`
}

type AdminQMXAutoSignSettingsRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type AdminQMXAutoSignLocationRequest struct {
	LocationName  string  `json:"location_name" binding:"required"`
	LocationIndex int     `json:"location_index"`
	Longitude     float64 `json:"longitude"`
	Latitude      float64 `json:"latitude"`
	Range         int     `json:"range"`
}

type AdminQMXAutoSignAccountRequest struct {
	Enabled  *bool                            `json:"enabled" binding:"required"`
	Location *AdminQMXAutoSignLocationRequest `json:"location"`
}
