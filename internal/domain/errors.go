package domain

import "errors"

// UserFacingError carries a message meant for the owner (and the model) as-is.
type UserFacingError struct{ Msg string }

func (e *UserFacingError) Error() string { return e.Msg }

// UserError builds a UserFacingError.
func UserError(msg string) error { return &UserFacingError{Msg: msg} }

// NotConnectedError means an integration has not been set up on the Settings page yet.
type NotConnectedError struct{ Msg string }

func (e *NotConnectedError) Error() string { return e.Msg }

var (
	ErrTaskNotConnected   = &NotConnectedError{"ยังไม่ได้เชื่อมระบบจัดการงาน ไปที่หน้าตั้งค่า > การเชื่อมต่อ ก่อนนะครับ"}
	ErrGoogleNotConnected = &NotConnectedError{"ยังไม่ได้เชื่อม Google Workspace ไปที่หน้าตั้งค่า > การเชื่อมต่อ ก่อนนะครับ"}
	ErrNotionNotConnected = &NotConnectedError{"ยังไม่ได้เชื่อม Notion ไปที่หน้าตั้งค่า > การเชื่อมต่อ ก่อนนะครับ"}
	ErrNoDatabase         = UserError("ฟีเจอร์นี้ต้องมีฐานข้อมูล PostgreSQL (ตั้ง DATABASE_URL)")
)

// IsExpected reports errors that are normal outcomes (bad input, missing setup), not bugs.
func IsExpected(err error) bool {
	var u *UserFacingError
	var n *NotConnectedError
	return errors.As(err, &u) || errors.As(err, &n)
}

// StatusError is an HTTP error from an external API.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string { return e.Msg }

// HTTPStatus returns the status of a StatusError, or 0.
func HTTPStatus(err error) int {
	var s *StatusError
	if errors.As(err, &s) {
		return s.Status
	}
	return 0
}
