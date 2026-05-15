package model

import "time"

type User struct {
	ID               uint      `gorm:"primaryKey" json:"-"`
	UID              int64     `gorm:"unique;not null" json:"uid"`
	Mobile           string    `gorm:"size:32;unique;not null" json:"mobile"`
	Name             string    `gorm:"size:128;not null" json:"name"`
	Avatar           string    `gorm:"size:1024" json:"avatar"`
	CredentialCipher string    `gorm:"type:text;not null" json:"-"`
	Permission       int       `gorm:"not null;default:1" json:"permission"`
	LastLoginAt      time.Time `json:"-"`
	CreatedAt        time.Time `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

type Whitelist struct {
	ID         uint      `gorm:"primaryKey" json:"-"`
	Mobile     string    `gorm:"size:32;unique;not null" json:"Mobile"`
	Permission int       `gorm:"not null;default:1" json:"Permission"`
	CreatedAt  time.Time `json:"-"`
	UpdatedAt  time.Time `json:"-"`
}

type Course struct {
	ID        uint      `gorm:"primaryKey" json:"-"`
	CourseID  int64     `gorm:"not null;uniqueIndex:idx_course_class" json:"course_id"`
	ClassID   int64     `gorm:"not null;uniqueIndex:idx_course_class" json:"class_id"`
	Name      string    `gorm:"size:255;not null" json:"name"`
	Teacher   string    `gorm:"size:255" json:"teacher"`
	Icon      string    `gorm:"size:1024" json:"icon"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

type UserCourse struct {
	ID         uint      `gorm:"primaryKey" json:"-"`
	UserUID    int64     `gorm:"not null;uniqueIndex:idx_user_course_class" json:"-"`
	CourseID   int64     `gorm:"not null;uniqueIndex:idx_user_course_class" json:"course_id"`
	ClassID    int64     `gorm:"not null;uniqueIndex:idx_user_course_class" json:"class_id"`
	IsSelected bool      `gorm:"not null;default:false" json:"is_selected"`
	CreatedAt  time.Time `json:"-"`
	UpdatedAt  time.Time `json:"-"`
}

type ClassGroup struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:128;not null" json:"name"`
	Description string    `gorm:"size:512" json:"description"`
	CreatedAt   time.Time `json:"-"`
	UpdatedAt   time.Time `json:"-"`
}

type ClassGroupMember struct {
	ID        uint      `gorm:"primaryKey" json:"-"`
	GroupID   uint      `gorm:"not null;index" json:"group_id"`
	UserUID   int64     `gorm:"not null;uniqueIndex" json:"user_uid"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

type SignActivity struct {
	ID           uint      `gorm:"primaryKey" json:"-"`
	ActivityID   int64     `gorm:"unique;not null" json:"activity_id"`
	StartTime    int64     `gorm:"not null" json:"start_time"`
	EndTime      int64     `gorm:"not null" json:"end_time"`
	SignType     int       `gorm:"not null" json:"sign_type"`
	IfRefreshEWM bool      `gorm:"not null;default:false" json:"if_refresh_ewm"`
	CreatedAt    time.Time `json:"-"`
	UpdatedAt    time.Time `json:"-"`
}

type SignShare struct {
	ID            uint       `gorm:"primaryKey" json:"-"`
	TokenHash     string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	CreatorUID    int64      `gorm:"not null;index" json:"creator_uid"`
	ActivityID    int64      `gorm:"not null;index" json:"activity_id"`
	CourseID      int64      `gorm:"not null" json:"course_id"`
	ClassID       int64      `gorm:"not null" json:"class_id"`
	SignType      int        `gorm:"not null" json:"sign_type"`
	IfRefreshEWM  bool       `gorm:"not null;default:false" json:"if_refresh_ewm"`
	ActivityName  string     `gorm:"size:255;not null" json:"activity_name"`
	CourseName    string     `gorm:"size:255;not null" json:"course_name"`
	CourseTeacher string     `gorm:"size:255" json:"course_teacher"`
	ExpiresAt     time.Time  `gorm:"not null;index" json:"expires_at"`
	UsedAt        *time.Time `json:"used_at"`
	CreatedAt     time.Time  `json:"-"`
	UpdatedAt     time.Time  `json:"-"`
}

type SignRecord struct {
	ID         uint      `gorm:"primaryKey" json:"-"`
	UserUID    int64     `gorm:"not null;uniqueIndex:idx_user_activity" json:"user_uid"`
	ActivityID int64     `gorm:"not null;uniqueIndex:idx_user_activity" json:"activity_id"`
	SourceUID  int64     `gorm:"not null" json:"source_uid"`
	SignTimeMS int64     `gorm:"not null" json:"sign_time_ms"`
	CreatedAt  time.Time `json:"-"`
	UpdatedAt  time.Time `json:"-"`
}
